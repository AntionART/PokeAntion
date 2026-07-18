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
  const username = `chatwatch_${suffix}`;
  await register(username, 'watch123456');

  const ws = new WebSocket(WS_URL);
  ws.on('open', () => send(envelope('login', { username, password: 'watch123456' }, 1), ws));

  console.log('Escuchando chat_message en littleroot_town (hasta 60s)...');
  const timeout = setTimeout(() => {
    console.error('TIMEOUT: no llegó ningún chat_message de client_dev.');
    process.exit(1);
  }, 60000);

  ws.on('message', (data) => {
    const msg = JSON.parse(data.toString());
    if (msg.type === 'login_ok') console.log('watcher conectado, character_id:', msg.payload.character_id);
    if (msg.type === 'chat_message') {
      console.log('chat_message recibido:', msg.payload);
      if (msg.payload.from_nickname === 'ClientDev') {
        clearTimeout(timeout);
        console.log('\nOK: el mensaje escrito en la UI del cliente real llegó por el servidor a otro jugador.');
        ws.close();
        process.exit(0);
      }
    }
  });
}

main().catch((e) => { console.error('FAILED:', e); process.exit(1); });
