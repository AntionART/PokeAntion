import { WebSocket } from 'ws';
import { randomUUID } from 'node:crypto';

const HTTP = process.env.HTTP_HOST || 'http://localhost:8080';
const WS_URL = process.env.WS_URL || 'ws://localhost:8080/ws';

async function register(username, password) {
  await fetch(`${HTTP}/register`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, email: `${username}@example.com`, password, rom_id: 'emerald_es', nickname: username })
  });
}

function send(env, ws) { ws.send(JSON.stringify(env)); }
function envelope(type, payload, seq = 0) { return { type, seq, payload }; }

function waitFor(ws, predicate, timeoutMs = 5000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('timeout esperando mensaje')), timeoutMs);
    function handler(data) {
      const msg = JSON.parse(data.toString());
      if (predicate(msg)) { clearTimeout(timer); ws.off('message', handler); resolve(msg); }
    }
    ws.on('message', handler);
  });
}

function connectAndLogin(username, password) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(WS_URL);
    ws.on('open', () => send(envelope('login', { username, password }, 1), ws));
    ws.on('error', reject);
    waitFor(ws, (m) => m.type === 'login_ok', 5000).then((msg) => resolve({ ws, payload: msg.payload })).catch(reject);
  });
}

async function main() {
  const suffix = randomUUID().slice(0, 6);
  const moverUser = `mover_${suffix}`, watcherUser = `watcher_${suffix}`;
  await register(moverUser, 'speed123456');
  await register(watcherUser, 'watch123456');

  // player_update excluye al emisor (a diferencia del chat local) — hace falta un segundo
  // cliente en el mismo mapa para verlo. move_rejected sí se manda directo al emisor.
  const mover = await connectAndLogin(moverUser, 'speed123456');
  const watcher = await connectAndLogin(watcherUser, 'watch123456');
  console.log('spawn inicial del mover:', mover.payload.map_id, mover.payload.pos_x, mover.payload.pos_y);

  console.log('\n=== 1. Primer movimiento (sin LastMoveAt previo) — debe aceptarse aunque sea "lejos" ===');
  send(envelope('move', { map_id: 'littleroot_town', x: 11, y: 11, facing: 'down', state: 'walking' }, 2), mover.ws);
  const firstUpdate = await waitFor(watcher.ws, (m) => m.type === 'player_update');
  console.log('watcher vio:', firstUpdate.payload);

  console.log('\n=== 2. Movimiento normal (1 tile) — debe aceptarse ===');
  send(envelope('move', { map_id: 'littleroot_town', x: 12, y: 11, facing: 'right', state: 'walking' }, 3), mover.ws);
  const normalMove = await waitFor(watcher.ws, (m) => m.type === 'player_update');
  console.log('watcher vio:', normalMove.payload);
  if (normalMove.payload.x !== 12) throw new Error('FALLO: el movimiento normal no llegó con la posición esperada');

  console.log('\n=== 3. Teletransporte obvio (200 tiles en el mismo instante) — debe RECHAZARSE ===');
  const rejectedPromise = waitFor(mover.ws, (m) => m.type === 'move_rejected');
  // El watcher NO debería recibir ningún player_update para este intento; lo detectamos
  // dejando pasar un margen corto sin mensajes en vez de esperar uno que no debe llegar.
  let watcherSawTeleport = false;
  const watcherHandler = (data) => {
    const msg = JSON.parse(data.toString());
    if (msg.type === 'player_update' && msg.payload.x === 212) watcherSawTeleport = true;
  };
  watcher.ws.on('message', watcherHandler);

  send(envelope('move', { map_id: 'littleroot_town', x: 212, y: 11, facing: 'right', state: 'walking' }, 4), mover.ws);
  const teleport = await rejectedPromise;
  await new Promise((r) => setTimeout(r, 300));
  watcher.ws.off('message', watcherHandler);

  console.log('mover recibió move_rejected:', teleport.payload);
  if (teleport.payload.x !== 12 || teleport.payload.y !== 11) {
    throw new Error(`FALLO: move_rejected debería corregir a la última posición válida (12,11), vino (${teleport.payload.x},${teleport.payload.y})`);
  }
  if (watcherSawTeleport) {
    throw new Error('FALLO: el watcher vio el teletransporte propagado como player_update — no debería haberse aceptado');
  }
  console.log('OK: el teletransporte fue rechazado, corregido a la última posición válida, y NUNCA se propagó a otros jugadores.');

  console.log('\n=== 4. Tras el rechazo, un movimiento normal vuelve a funcionar ===');
  send(envelope('move', { map_id: 'littleroot_town', x: 13, y: 11, facing: 'right', state: 'walking' }, 5), mover.ws);
  const recovered = await waitFor(watcher.ws, (m) => m.type === 'player_update');
  console.log('watcher vio:', recovered.payload);
  if (recovered.payload.x !== 13) throw new Error('FALLO: el movimiento normal tras un rechazo no se propagó correctamente');

  console.log('\nTodos los escenarios de límite de velocidad pasaron.');
  mover.ws.close(); watcher.ws.close();
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
