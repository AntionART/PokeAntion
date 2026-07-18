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

async function main() {
  const suffix = randomUUID().slice(0, 6);
  const username = `fakefriend_${suffix}`;
  await register(username, 'fake123456');

  const ws = new WebSocket(WS_URL);
  let seq = 2;
  ws.on('open', () => send(envelope('login', { username, password: 'fake123456' }, 1), ws));
  ws.on('message', (data) => {
    const msg = JSON.parse(data.toString());
    if (msg.type === 'login_ok') {
      console.log('fake-friend logueado, character_id:', msg.payload.character_id);
    }
  });

  // No hay snapshot inicial al loguearse otro jugador (limitación conocida del protocolo),
  // así que hay que seguir mandando "move" periódicamente para que cualquiera que se
  // conecte DESPUÉS también lo vea. Se para 3 tiles a la derecha y 2 abajo de (11,11).
  const x = 14, y = 13;
  const interval = setInterval(() => {
    if (ws.readyState === WebSocket.OPEN) {
      send(envelope('move', { map_id: 'littleroot_town', x, y, facing: 'down', state: 'idle' }, seq++), ws);
    }
  }, 1000);

  await new Promise((r) => setTimeout(r, 25000));
  clearInterval(interval);
  ws.close();
}

main().catch((e) => { console.error('FAILED:', e); process.exit(1); });
