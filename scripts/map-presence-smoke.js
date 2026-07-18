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
  const earlyUser = `early_${suffix}`, lateUser = `late_${suffix}`, thirdUser = `third_${suffix}`;
  await register(earlyUser, 'pass123456');
  await register(lateUser, 'pass123456');
  await register(thirdUser, 'pass123456');

  console.log('=== 1. "early" se conecta y se mueve, para tener una posición real en el mapa ===');
  const early = await connectAndLogin(earlyUser, 'pass123456');
  send(envelope('move', { map_id: 'littleroot_town', x: 15, y: 20, facing: 'down', state: 'idle' }, 2), early.ws);
  await new Promise((r) => setTimeout(r, 300)); // darle tiempo al servidor a procesar el move

  console.log('\n=== 2. "late" se conecta DESPUÉS — debería recibir un snapshot con "early" ya en el mapa ===');
  const latePromise = new Promise((resolve, reject) => {
    const ws = new WebSocket(WS_URL);
    let loggedIn = false;
    ws.on('open', () => send(envelope('login', { username: lateUser, password: 'pass123456' }, 1), ws));
    ws.on('error', reject);
    const timer = setTimeout(() => reject(new Error('timeout esperando map_players_snapshot')), 5000);
    ws.on('message', (data) => {
      const msg = JSON.parse(data.toString());
      if (msg.type === 'login_ok') loggedIn = true;
      if (msg.type === 'map_players_snapshot') {
        clearTimeout(timer);
        resolve({ ws, snapshot: msg.payload });
      }
    });
  });
  const { ws: lateWs, snapshot } = await latePromise;
  console.log('snapshot recibido por "late":', snapshot);
  const earlyInSnapshot = snapshot.players.find((p) => p.x === 15 && p.y === 20);
  if (!earlyInSnapshot) throw new Error('FALLO: "late" no vio a "early" en el snapshot inicial');
  console.log('OK: el snapshot inicial incluyó a un jugador que ya estaba en el mapa.');

  console.log('\n=== 3. "third" se conecta y observa; "early" se desconecta -> "third" debe ver player_left_map ===');
  const third = await connectAndLogin(thirdUser, 'pass123456');
  await new Promise((r) => setTimeout(r, 200));
  const leftPromise = waitFor(third.ws, (m) => m.type === 'player_left_map');
  early.ws.close();
  const left = await leftPromise;
  console.log('third recibió player_left_map:', left.payload);
  console.log('OK: la desconexión de un tercero se propagó como player_left_map.');

  console.log('\nTodos los escenarios de presencia en el mapa pasaron.');
  lateWs.close();
  third.ws.close();
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
