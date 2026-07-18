import { WebSocket } from 'ws';
import { randomUUID } from 'node:crypto';

const HTTP = process.env.HTTP_HOST || 'http://localhost:8080';
const WS_URL = process.env.WS_URL || 'ws://localhost:8080/ws';

async function register(username, password) {
  const res = await fetch(`${HTTP}/register`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, email: `${username}@example.com`, password, rom_id: 'emerald_es', nickname: username })
  });
  return res.ok;
}

function send(env, ws) { ws.send(JSON.stringify(env)); }
function envelope(type, payload, seq = 0) { return { type, seq, payload }; }

async function main() {
  const suffix = randomUUID().slice(0, 6);
  const username = `watcher_${suffix}`;
  await register(username, 'watcher123');

  const ws = new WebSocket(WS_URL);
  ws.on('open', () => send(envelope('login', { username, password: 'watcher123' }, 1), ws));

  console.log('Escuchando player_update en littleroot_town, esperando al cliente real (client_dev)...');
  const timeout = setTimeout(() => {
    console.error('TIMEOUT: no llegó ningún player_update de client_dev en 30s.');
    console.error('¿Está corriendo ClientApp conectado al servidor? ¿Se movió el personaje?');
    process.exit(1);
  }, 30000);

  ws.on('message', (data) => {
    const msg = JSON.parse(data.toString());
    if (msg.type === 'login_ok') {
      console.log('watcher conectado, character_id:', msg.payload.character_id);
    }
    if (msg.type === 'player_update') {
      console.log('player_update recibido:', msg.payload);
      clearTimeout(timeout);
      console.log('\nOK: la posición real del cliente (leída de la ROM) llegó sincronizada a otro jugador conectado.');
      ws.close();
      process.exit(0);
    }
  });
}

main().catch((e) => { console.error('FAILED:', e); process.exit(1); });
