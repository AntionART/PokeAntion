import { WebSocket } from 'ws';
import { randomUUID } from 'node:crypto';

const HTTP = process.env.HTTP_HOST || 'http://localhost:8080';
const WS_URL = process.env.WS_URL || 'ws://localhost:8080/ws';

async function register(username, password) {
  const res = await fetch(`${HTTP}/register`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, email: `${username}@example.com`, password, rom_id: 'emerald_us', nickname: username })
  });
  if (!res.ok) throw new Error(`register ${username} failed: ${res.status} ${await res.text()}`);
  return await res.json();
}

function send(env, ws) { ws.send(JSON.stringify(env)); }
function envelope(type, payload, seq = 0) { return { type, seq, payload }; }

function connectAndLogin(username, password) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(WS_URL);
    const messages = [];
    ws.on('message', (data) => messages.push(JSON.parse(data.toString())));
    ws.on('open', () => send(envelope('login', { username, password }, 1), ws));
    ws.on('error', reject);
    const timer = setTimeout(() => reject(new Error('timeout en login')), 5000);
    const check = setInterval(() => {
      const ok = messages.find((m) => m.type === 'login_ok');
      if (ok) { clearInterval(check); clearTimeout(timer); resolve({ ws, messages, characterId: ok.payload.character_id, name: username, loginOk: ok.payload }); }
    }, 50);
  });
}

function waitFor(session, predicate, timeoutMs = 5000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`timeout esperando mensaje en ${session.name}: ` + JSON.stringify(session.messages.slice(-5)))), timeoutMs);
    function handler(data) {
      const msg = JSON.parse(data.toString());
      if (predicate(msg)) { clearTimeout(timer); session.ws.off('message', handler); resolve(msg); }
    }
    session.ws.on('message', handler);
  });
}

async function main() {
  const suffix = randomUUID().slice(0, 8);
  const names = ['painter', 'watcher'].map((n) => `${n}_${suffix}`);
  for (const n of names) await register(n, 'pass1234');

  const painter = await connectAndLogin(names[0], 'pass1234');
  const watcher = await connectAndLogin(names[1], 'pass1234');

  console.log('=== 1. login_ok trae color "default" para una cuenta nueva ===');
  if (painter.loginOk.color !== 'default') throw new Error(`FALLO: color inicial esperado "default", vino "${painter.loginOk.color}"`);
  console.log('OK: color inicial = default.');

  console.log('\n=== 2. painter y watcher entran al mismo mapa (move) para que se vean ===');
  send(envelope('move', { map_id: 'littleroot_town', x: 10, y: 12, facing: 'down', state: 'idle' }, 2), painter.ws);
  send(envelope('move', { map_id: 'littleroot_town', x: 11, y: 12, facing: 'down', state: 'idle' }, 2), watcher.ws);
  await new Promise((r) => setTimeout(r, 300));

  console.log('\n=== 3. painter cambia a "red": debe recibir confirmación Y watcher debe verlo en vivo ===');
  const paintersOwnUpdateP = waitFor(painter, (m) => m.type === 'player_update' && m.payload.character_id === painter.characterId && m.payload.color === 'red');
  const watcherSeesP = waitFor(watcher, (m) => m.type === 'player_update' && m.payload.character_id === painter.characterId && m.payload.color === 'red');
  send(envelope('set_color', { color: 'red' }, 3), painter.ws);
  const [selfConfirm, watcherSees] = await Promise.all([paintersOwnUpdateP, watcherSeesP]);
  if (selfConfirm.payload.color !== 'red') throw new Error('FALLO: painter no recibió confirmación de su propio color');
  if (watcherSees.payload.color !== 'red') throw new Error('FALLO: watcher no vio el color nuevo de painter');
  console.log('OK: confirmación al emisor y broadcast al mapa, ambos con color "red".');

  console.log('\n=== 4. un color fuera de la paleta permitida debe rechazarse ===');
  send(envelope('set_color', { color: 'invisible-hack' }, 4), painter.ws);
  const rejected = await waitFor(painter, (m) => m.type === 'error');
  console.log('error esperado:', rejected.payload);
  if (rejected.payload.code !== 'invalid_state') throw new Error('FALLO: se esperaba invalid_state para un color no permitido');

  console.log('\n=== 5. moverse después de cambiar de color debe seguir llevando nickname Y color en el player_update (bug real: antes viajaban vacíos) ===');
  // Un solo tile (no una distancia grande): el límite de velocidad del servidor se calcula
  // sobre el tiempo transcurrido desde el último "move" ACEPTADO (paso 2), y los pasos 3/4 de
  // este test no vuelven a mover a painter — probar con una distancia grande acá arriesga
  // pisar el límite de tiles/segundo por timing del propio test, no del servidor.
  const moveUpdateP = waitFor(watcher, (m) => m.type === 'player_update' && m.payload.character_id === painter.characterId && m.payload.x === 11);
  send(envelope('move', { map_id: 'littleroot_town', x: 11, y: 12, facing: 'right', state: 'walking' }, 5), painter.ws);
  const moveUpdate = await moveUpdateP;
  if (moveUpdate.payload.nickname !== names[0]) throw new Error(`FALLO: nickname vacío en player_update de movimiento (vino "${moveUpdate.payload.nickname}")`);
  if (moveUpdate.payload.color !== 'red') throw new Error(`FALLO: color no persistió en player_update de movimiento (vino "${moveUpdate.payload.color}")`);
  console.log('OK: el player_update de un movimiento normal ahora trae nickname y color correctos.');

  console.log('\n=== 6. painter se reconecta -> login_ok debe traer el color persistido ("red"), no "default" ===');
  painter.ws.close();
  await new Promise((r) => setTimeout(r, 300));
  const painter2 = await connectAndLogin(names[0], 'pass1234');
  if (painter2.loginOk.color !== 'red') throw new Error(`FALLO: color no persistió tras reconectar (vino "${painter2.loginOk.color}")`);
  console.log('OK: el color elegido persiste entre sesiones.');

  console.log('\nTodos los escenarios de color pasaron.');
  painter2.ws.close(); watcher.ws.close();
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
