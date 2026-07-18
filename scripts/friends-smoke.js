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

function connectAndLogin(username, password) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(WS_URL);
    const messages = [];
    ws.on('message', (data) => {
      const msg = JSON.parse(data.toString());
      messages.push(msg);
    });
    ws.on('open', () => send(envelope('login', { username, password }, 1), ws));
    ws.on('error', reject);
    const timer = setTimeout(() => reject(new Error('timeout en login')), 5000);
    const check = setInterval(() => {
      const ok = messages.find((m) => m.type === 'login_ok');
      if (ok) { clearInterval(check); clearTimeout(timer); resolve({ ws, messages, characterId: ok.payload.character_id, accountId: ok.payload.account_id }); }
    }, 50);
  });
}

function waitFor(session, predicate, timeoutMs = 5000) {
  return new Promise((resolve, reject) => {
    const already = session.messages.find(predicate);
    if (already) return resolve(already);
    const timer = setTimeout(() => reject(new Error('timeout esperando mensaje: ' + JSON.stringify(session.messages.slice(-5)))), timeoutMs);
    function handler(data) {
      const msg = JSON.parse(data.toString());
      if (predicate(msg)) { clearTimeout(timer); session.ws.off('message', handler); resolve(msg); }
    }
    session.ws.on('message', handler);
  });
}

async function main() {
  const suffix = randomUUID().slice(0, 8);
  const ashUser = `ash_f_${suffix}`, mistyUser = `misty_f_${suffix}`;
  await register(ashUser, 'pikachu123');
  await register(mistyUser, 'squirtle123');

  const ash = await connectAndLogin(ashUser, 'pikachu123');
  const misty = await connectAndLogin(mistyUser, 'squirtle123');
  console.log('ash account_id:', ash.accountId, '| misty account_id:', misty.accountId);

  console.log('\n=== 1. ash envía friend_request a misty (por username) ===');
  send(envelope('friend_request', { target_username: mistyUser }, 1), ash.ws);
  const received = await waitFor(misty, (m) => m.type === 'friend_request_received');
  console.log('misty recibió friend_request_received:', received.payload);
  if (received.payload.from_account_id !== ash.accountId) throw new Error('FALLO: from_account_id no coincide con ash');

  console.log('\n=== 2. friend_request duplicado debe fallar (ya hay una pendiente) ===');
  send(envelope('friend_request', { target_username: mistyUser }, 2), ash.ws);
  const dupErr = await waitFor(ash, (m) => m.type === 'error');
  console.log('error esperado:', dupErr.payload);
  if (dupErr.payload.code !== 'invalid_state') throw new Error('FALLO: se esperaba error invalid_state en solicitud duplicada');

  console.log('\n=== 3. misty acepta ===');
  send(envelope('friend_accept', { target_account_id: ash.accountId }, 3), misty.ws);
  const statusUpdate = await waitFor(ash, (m) => m.type === 'friend_status_update');
  console.log('ash recibió friend_status_update tras el accept:', statusUpdate.payload);
  if (statusUpdate.payload.account_id !== misty.accountId || statusUpdate.payload.online !== true) {
    throw new Error('FALLO: friend_status_update inesperado tras accept');
  }

  console.log('\n=== 4. friend_list de ambos lados ===');
  send(envelope('friend_list', {}, 4), ash.ws);
  send(envelope('friend_list', {}, 4), misty.ws);
  const ashList = await waitFor(ash, (m) => m.type === 'friend_list');
  const mistyList = await waitFor(misty, (m) => m.type === 'friend_list');
  console.log('friend_list de ash:', ashList.payload);
  console.log('friend_list de misty:', mistyList.payload);
  const ashSeesMisty = ashList.payload.friends.find((f) => f.account_id === misty.accountId);
  const mistySeesAsh = mistyList.payload.friends.find((f) => f.account_id === ash.accountId);
  if (!ashSeesMisty || !ashSeesMisty.online) throw new Error('FALLO: ash no ve a misty como amiga online');
  if (!mistySeesAsh || !mistySeesAsh.online) throw new Error('FALLO: misty no ve a ash como amigo online (la amistad debería ser bidireccional)');
  console.log('OK: la amistad quedó registrada en ambas direcciones, ambos se ven online.');

  console.log('\n=== 5. misty se desconecta -> ash debe recibir friend_status_update online=false ===');
  const offlinePromise = waitFor(ash, (m) => m.type === 'friend_status_update' && m.payload.online === false);
  misty.ws.close();
  const offline = await offlinePromise;
  console.log('ash recibió:', offline.payload);
  if (offline.payload.account_id !== misty.accountId) throw new Error('FALLO: notificación offline con account_id incorrecto');
  console.log('OK: la desconexión de un amigo notifica al otro en tiempo real.');

  console.log('\n=== 6. ash elimina la amistad (verificado contra la base de datos) ===');
  send(envelope('friend_remove', { target_account_id: misty.accountId }, 6), ash.ws);
  await new Promise((r) => setTimeout(r, 400));
  const remaining = psql(
    `SELECT count(*) FROM friendships WHERE (account_id = '${ash.accountId}' AND friend_id = '${misty.accountId}')
        OR (account_id = '${misty.accountId}' AND friend_id = '${ash.accountId}');`
  );
  console.log('filas de friendships restantes entre ash y misty:', remaining);
  if (remaining !== '0') throw new Error('FALLO: friend_remove no borró la amistad en ambas direcciones');
  console.log('OK: friend_remove eliminó la relación en ambas direcciones.');

  console.log('\nTodos los escenarios de amigos pasaron.');

  ash.ws.close();
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
