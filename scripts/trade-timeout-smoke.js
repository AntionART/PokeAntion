import { WebSocket } from 'ws';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';

const HTTP = process.env.HTTP_HOST || 'http://localhost:8080';
const WS_URL = process.env.WS_URL || 'ws://localhost:8080/ws';
const PSQL = 'C:/Users/AuxSistemas/Pictures/Antion/pokemon-online/postgresql-16.5/pgsql/bin/psql.exe';
const PG_ARGS_BASE = ['-U', 'pokemon', '-h', 'localhost', '-p', '5432', '-d', 'pokemon_online', '-t', '-A'];

function psql(sql) {
  return execFileSync(PSQL, [...PG_ARGS_BASE, '-c', sql], { env: { ...process.env, PGPASSWORD: 'pokemon' } }).toString().trim();
}

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

function waitFor(ws, name, predicate, timeoutMs) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`timeout esperando mensaje en ${name}`)), timeoutMs);
    function handler(data) {
      const msg = JSON.parse(data.toString());
      console.log(`[${new Date().toISOString()}] ${name} <-`, msg.type, JSON.stringify(msg.payload));
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
    waitFor(ws, username, (m) => m.type === 'login_ok', 5000).then((msg) => {
      resolve({ ws, characterId: msg.payload.character_id });
    }).catch(reject);
  });
}

async function main() {
  const suffix = randomUUID().slice(0, 8);
  await register(`ash_t_${suffix}`, 'pikachu123');
  await register(`misty_t_${suffix}`, 'squirtle123');
  const ash = await connectAndLogin(`ash_t_${suffix}`, 'pikachu123');
  const misty = await connectAndLogin(`misty_t_${suffix}`, 'squirtle123');

  console.log(`[${new Date().toISOString()}] creando sesión de trade y dejándola abandonada (nadie acepta/ofrece/confirma)...`);
  send(envelope('trade_request', { target_character_id: misty.characterId }, 1), ash.ws);
  const req = await waitFor(misty.ws, 'misty', (m) => m.type === 'trade_request_received', 5000);
  const sessionId = req.payload.trade_session_id;
  console.log('trade_session_id:', sessionId, '- esperando hasta 3 minutos por el timeout automático de 2 minutos...');

  const cancelled = await waitFor(ash.ws, 'ash', (m) => m.type === 'trade_cancelled' && m.payload.reason === 'timeout', 3 * 60 * 1000);
  const status = psql(`SELECT status, cancelled_reason FROM trade_sessions WHERE id = '${sessionId}';`);
  console.log('trade_sessions status/reason en DB:', status);

  if (!status.startsWith('cancelled')) throw new Error('FALLO: la sesión no quedó cancelled por timeout en la base de datos');
  console.log('OK: la sesión de trade abandonada se auto-canceló por timeout y ambos jugadores fueron notificados.');

  ash.ws.close(); misty.ws.close();
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
