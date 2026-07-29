// Smoke test de carga concurrente: simula N jugadores reales (cuenta propia, WS propia)
// conectados AL MISMO TIEMPO, todos en el mismo mapa, moviéndose y chateando en paralelo
// durante varios segundos — el escenario real de "varios amigos jugando juntos". No es un
// benchmark de rendimiento (no mide latencia con precisión), es una prueba de que el server
// no se cae/deadlockea/pierde conexiones bajo concurrencia real, cosa que ningún smoke test
// anterior (todos de a 1-2 jugadores) llegó a ejercitar.
import { WebSocket } from 'ws';
import { randomUUID } from 'node:crypto';

const HTTP = process.env.HTTP_HOST || 'http://localhost:8080';
const WS_URL = process.env.WS_URL || 'ws://localhost:8080/ws';
const N = parseInt(process.env.LOAD_N || '30', 10);
const DURATION_MS = parseInt(process.env.LOAD_DURATION_MS || '8000', 10);

function envelope(type, payload, seq = 0) { return { type, seq, payload }; }

async function register(username) {
  const res = await fetch(`${HTTP}/register`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, email: `${username}@example.com`, password: 'loadtest123', rom_id: 'emerald_es', nickname: username, starter_species: 280 })
  });
  if (!res.ok) throw new Error(`register ${username} falló: ${res.status} ${await res.text()}`);
  return await res.json();
}

function connectAndLogin(username) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(WS_URL);
    let settled = false;
    const timer = setTimeout(() => { if (!settled) { settled = true; reject(new Error(`${username}: timeout conectando/logueando`)); } }, 10000);

    ws.on('open', () => ws.send(JSON.stringify(envelope('login', { username, password: 'loadtest123' }, 1))));
    ws.on('error', (e) => { if (!settled) { settled = true; clearTimeout(timer); reject(e); } });
    ws.on('message', (data) => {
      if (settled) return;
      const msg = JSON.parse(data.toString());
      if (msg.type === 'login_ok') { settled = true; clearTimeout(timer); resolve({ ws, characterId: msg.payload.character_id }); }
      else if (msg.type === 'login_error') { settled = true; clearTimeout(timer); reject(new Error(`${username}: login_error`)); }
    });
  });
}

async function main() {
  console.log(`=== Carga concurrente: ${N} jugadores reales conectados a la vez, ${DURATION_MS}ms de movimiento+chat simultáneo ===`);
  const runId = randomUUID().slice(0, 8);
  const usernames = Array.from({ length: N }, (_, i) => `load_${runId}_${i}`);

  console.log(`\n=== 1. Registrar ${N} cuentas (en paralelo) ===`);
  const t0 = Date.now();
  const regResults = await Promise.allSettled(usernames.map(register));
  const regFailed = regResults.filter(r => r.status === 'rejected');
  if (regFailed.length > 0) {
    console.error(`FALLO: ${regFailed.length}/${N} registros fallaron:`, regFailed.slice(0, 3).map(r => r.reason?.message));
    process.exit(1);
  }
  console.log(`OK: ${N} cuentas registradas en ${Date.now() - t0}ms`);

  console.log(`\n=== 2. Conectar + login de ${N} WebSockets (en paralelo) ===`);
  const t1 = Date.now();
  const connResults = await Promise.allSettled(usernames.map(connectAndLogin));
  const connFailed = connResults.filter(r => r.status === 'rejected');
  if (connFailed.length > 0) {
    console.error(`FALLO: ${connFailed.length}/${N} conexiones/logins fallaron:`, connFailed.slice(0, 5).map(r => r.reason?.message));
    process.exit(1);
  }
  const sessions = connResults.map(r => r.value);
  console.log(`OK: ${N} jugadores conectados y logueados en ${Date.now() - t1}ms`);

  console.log(`\n=== 3. Reportar a todos en el mismo mapa (MAP_ROUTE101) ===`);
  for (const s of sessions) {
    s.ws.send(JSON.stringify(envelope('move', { map_id: 'MAP_ROUTE101', x: 10, y: 10, facing: 'down', state: 'walking' }, 2)));
  }
  await new Promise((r) => setTimeout(r, 300));

  console.log(`\n=== 4. Movimiento + chat simultáneo de los ${N} jugadores durante ${DURATION_MS}ms ===`);
  let disconnected = 0, errors = 0, chatReceivedCount = 0;
  for (const s of sessions) {
    s.ws.on('close', () => disconnected++);
    s.ws.on('error', () => errors++);
    s.ws.on('message', (data) => {
      try { if (JSON.parse(data.toString()).type === 'chat_message') chatReceivedCount++; } catch { /* ignorar */ }
    });
  }

  const t2 = Date.now();
  let seq = 10, x = 10, y = 10;
  const moveInterval = setInterval(() => {
    seq++;
    x = 10 + (seq % 5); // movimiento chico, dentro del límite de velocidad real del servidor
    for (const s of sessions) {
      if (s.ws.readyState === WebSocket.OPEN) {
        s.ws.send(JSON.stringify(envelope('move', { map_id: 'MAP_ROUTE101', x, y, facing: 'down', state: 'walking' }, seq)));
      }
    }
  }, 200);

  const chatInterval = setInterval(() => {
    // Cada jugador manda UN chat cada ~2s (no todos a la vez) para ejercitar rate limiting real
    // sin dispararlo — igual que jugadores reales conversando, no un flood.
    const s = sessions[Math.floor(Math.random() * sessions.length)];
    if (s.ws.readyState === WebSocket.OPEN) {
      s.ws.send(JSON.stringify(envelope('send_chat', { channel: 'local', text: `hola desde ${s.characterId.slice(0, 8)}` }, seq++)));
    }
  }, 250);

  await new Promise((r) => setTimeout(r, DURATION_MS));
  clearInterval(moveInterval);
  clearInterval(chatInterval);

  console.log(`Fin de la fase de carga (${Date.now() - t2}ms transcurridos): ${disconnected} desconexiones inesperadas, ${errors} errores de socket, ${chatReceivedCount} chat_message recibidos en total.`);

  console.log(`\n=== 5. Verificar que el servidor sigue respondiendo (/health) ===`);
  const health = await fetch(`${HTTP}/health`);
  const healthOk = health.ok;
  console.log(`/health -> ${health.status} (${healthOk ? 'OK' : 'FALLO'})`);

  // Evaluar ANTES de cerrar nada — cerrar nuestras propias conexiones a propósito en el paso 6
  // dispara el mismo evento 'close' que una caída real, así que contaría como "desconexión
  // inesperada" si se evaluara después (bug real de este script, encontrado en la primera
  // corrida: reportaba disconnected=N con el servidor totalmente sano).
  const success = disconnected === 0 && errors === 0 && healthOk && chatReceivedCount > 0;

  console.log(`\n=== 6. Cerrar todas las conexiones limpiamente ===`);
  for (const s of sessions) s.ws.close();
  await new Promise((r) => setTimeout(r, 500));
  if (success) {
    console.log(`\n=== TODO OK: ${N} jugadores concurrentes reales (registro+login+movimiento+chat simultáneo) sin caídas ni errores, servidor sigue sano. ===`);
  } else {
    console.error(`\nFALLO: disconnected=${disconnected} errors=${errors} healthOk=${healthOk} chatReceivedCount=${chatReceivedCount}`);
    process.exit(1);
  }
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
