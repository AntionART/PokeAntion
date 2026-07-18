import { WebSocket } from 'ws';
import { randomUUID } from 'node:crypto';

const HTTP = process.env.HTTP_HOST || 'http://localhost:8080';
const WS_URL = process.env.WS_URL || 'ws://localhost:8080/ws';

async function register(username, password) {
  const res = await fetch(`${HTTP}/register`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, email: `${username}@example.com`, password, rom_id: 'emerald_us', nickname: username })
  });
  if (!res.ok) throw new Error(`register failed: ${res.status} ${await res.text()}`);
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
      if (ok) { clearInterval(check); clearTimeout(timer); resolve({ ws, messages, characterId: ok.payload.character_id }); }
    }, 50);
  });
}

async function main() {
  const suffix = randomUUID().slice(0, 8);
  await register(`spammer_${suffix}`, 'pikachu123');
  const { ws, messages } = await connectAndLogin(`spammer_${suffix}`, 'pikachu123');

  console.log('Enviando 8 mensajes de chat en ráfaga (límite configurado: 5 por segundo)...');
  for (let i = 0; i < 8; i++) {
    send(envelope('send_chat', { channel: 'local', message: `msg ${i}` }, 10 + i), ws);
  }

  await new Promise((r) => setTimeout(r, 800));

  const chatEchoes = messages.filter((m) => m.type === 'chat_message');
  const rateLimitErrors = messages.filter((m) => m.type === 'error' && m.payload.code === 'rate_limited');
  console.log(`chat_message recibidos (local se difunde a todo el mapa, no hay eco directo al emisor): ${chatEchoes.length}`);
  console.log(`errores rate_limited recibidos: ${rateLimitErrors.length}`);
  console.log('payload de un error rate_limited:', rateLimitErrors[0]);

  if (rateLimitErrors.length === 0) {
    throw new Error('FALLO: se mandaron 8 mensajes en ráfaga con límite de 5/s y ninguno fue rechazado como rate_limited');
  }
  console.log(`OK: el rate limiter de Redis bloqueó ${rateLimitErrors.length} de los 8 mensajes en ráfaga.`);

  console.log('\nEsperando 1.2s para que expire la ventana...');
  await new Promise((r) => setTimeout(r, 1200));
  messages.length = 0;
  send(envelope('send_chat', { channel: 'local', message: 'deberia pasar' }, 99), ws);
  await new Promise((r) => setTimeout(r, 300));
  const afterWindow = messages.filter((m) => m.type === 'error' && m.payload.code === 'rate_limited');
  console.log(`errores rate_limited tras esperar la ventana (esperado 0): ${afterWindow.length}`);
  if (afterWindow.length !== 0) throw new Error('FALLO: seguía bloqueado después de que la ventana expiró');
  console.log('OK: pasada la ventana de 1s, el rate limit se resetea y el mensaje pasa normalmente.');

  ws.close();
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
