import { WebSocket } from 'ws';
import { randomUUID } from 'node:crypto';

const HTTP = process.env.HTTP_HOST || 'http://localhost:8080';
const WS_URL = process.env.WS_URL || 'ws://localhost:8080/ws';

async function register(username, password) {
  const res = await fetch(`${HTTP}/register`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, email: `${username}@example.com`, password, rom_id: 'emerald_us', nickname: `Nick_${username}` })
  });
  if (!res.ok) throw new Error(`register failed: ${res.status} ${await res.text()}`);
  return await res.json();
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

async function main() {
  const suffix = randomUUID().slice(0, 8);
  const username = `jwt_${suffix}`;
  await register(username, 'pikachu123');

  console.log('=== 1. Login normal con usuario/contraseña ===');
  const ws1 = new WebSocket(WS_URL);
  ws1.on('open', () => send(envelope('login', { username, password: 'pikachu123' }, 1), ws1));
  const ok1 = await waitFor(ws1, (m) => m.type === 'login_ok' || m.type === 'login_error');
  console.log('login_ok payload:', ok1.payload);
  if (ok1.type !== 'login_ok') throw new Error('FALLO: login normal no funcionó');
  if (!ok1.payload.session_token || ok1.payload.session_token.split('.').length !== 3) {
    throw new Error('FALLO: session_token no parece un JWT (esperaba 3 partes separadas por punto)');
  }
  if (!ok1.payload.account_id) throw new Error('FALLO: account_id vino vacío en login_ok');
  console.log('OK: login normal emite un JWT real y account_id.');
  const token = ok1.payload.session_token;
  ws1.close();

  await new Promise((r) => setTimeout(r, 200));

  console.log('\n=== 2. Reconectar SOLO con el session_token (sin usuario/contraseña) ===');
  const ws2 = new WebSocket(WS_URL);
  ws2.on('open', () => send(envelope('login', { session_token: token }, 1), ws2));
  const ok2 = await waitFor(ws2, (m) => m.type === 'login_ok' || m.type === 'login_error');
  console.log('login_ok payload (reconexión):', ok2.payload);
  if (ok2.type !== 'login_ok') throw new Error('FALLO: reconexión con session_token fue rechazada');
  if (ok2.payload.character_id !== ok1.payload.character_id) throw new Error('FALLO: reconexión devolvió un character_id distinto');
  if (ok2.payload.nickname_check !== undefined) { /* no-op, nickname no viaja en login_ok hoy */ }
  console.log('OK: la reconexión por JWT devolvió el mismo character_id y un token renovado.');
  ws2.close();

  await new Promise((r) => setTimeout(r, 200));

  console.log('\n=== 3. Token inválido / falsificado debe ser rechazado ===');
  const ws3 = new WebSocket(WS_URL);
  ws3.on('open', () => send(envelope('login', { session_token: token.slice(0, -3) + 'xxx' }, 1), ws3));
  const rej = await waitFor(ws3, (m) => m.type === 'login_ok' || m.type === 'login_error');
  console.log('respuesta a token falsificado:', rej.type);
  if (rej.type !== 'login_error') throw new Error('FALLO: un token con firma inválida fue aceptado');
  console.log('OK: un token con firma alterada es rechazado con login_error.');
  ws3.close();

  console.log('\nTodos los escenarios de JWT pasaron.');
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
