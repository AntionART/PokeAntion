import { WebSocket } from 'ws';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';

const HTTP = process.env.HTTP_HOST || 'http://localhost:8080';
const WS_URL = process.env.WS_URL || 'ws://localhost:8080/ws';
const PSQL = process.env.PSQL_PATH || 'C:/Users/AuxSistemas/Pictures/Antion/pokemon-online/postgresql-16.5/pgsql/bin/psql.exe';
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

function waitFor(ws, name, predicate, timeoutMs = 5000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`timeout esperando mensaje en ${name}`)), timeoutMs);
    function handler(data) {
      const msg = JSON.parse(data.toString());
      console.log(`${name} <-`, msg.type, JSON.stringify(msg.payload));
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

function insertMon(charId) {
  const id = randomUUID();
  const cols = `(id, owner_char_id, species_id, level, hp_current, hp_max, stat_attack, stat_defense, stat_sp_attack, stat_sp_defense, stat_speed, nature, location, team_slot)`;
  psql(`INSERT INTO pokemon ${cols} VALUES ('${id}', '${charId}', 1, 5, 20, 20, 10, 10, 10, 10, 10, 1, 'team', 0);`);
  return id;
}

async function scenarioDecline() {
  console.log('\n########## ESCENARIO 1: trade_decline ##########');
  const suffix = randomUUID().slice(0, 8);
  const ashReg = await register(`ash_d_${suffix}`, 'pikachu123');
  const mistyReg = await register(`misty_d_${suffix}`, 'squirtle123');
  const ash = await connectAndLogin(`ash_d_${suffix}`, 'pikachu123');
  const misty = await connectAndLogin(`misty_d_${suffix}`, 'squirtle123');

  send(envelope('trade_request', { target_character_id: misty.characterId }, 1), ash.ws);
  const req = await waitFor(misty.ws, 'misty', (m) => m.type === 'trade_request_received');
  const sessionId = req.payload.trade_session_id;

  const declineOnAsh = waitFor(ash.ws, 'ash', (m) => m.type === 'trade_cancelled');
  send(envelope('trade_decline', { trade_session_id: sessionId }, 2), misty.ws);
  await declineOnAsh;

  const status = psql(`SELECT status FROM trade_sessions WHERE id = '${sessionId}';`);
  console.log('trade_sessions.status tras decline:', status);
  if (status !== 'cancelled') throw new Error('FALLO: la sesión no quedó cancelled tras trade_decline');
  console.log('OK: trade_decline canceló la sesión y notificó al solicitante.');

  ash.ws.close(); misty.ws.close();
}

async function scenarioDisconnect() {
  console.log('\n########## ESCENARIO 2: desconexión a mitad de trade (con oferta puesta) ##########');
  const suffix = randomUUID().slice(0, 8);
  const ashReg = await register(`ash_x_${suffix}`, 'pikachu123');
  const mistyReg = await register(`misty_x_${suffix}`, 'squirtle123');
  const ash = await connectAndLogin(`ash_x_${suffix}`, 'pikachu123');
  const misty = await connectAndLogin(`misty_x_${suffix}`, 'squirtle123');

  const ashMon = insertMon(ash.characterId);
  console.log('pokemon de ash bloqueado en esta prueba:', ashMon);

  send(envelope('trade_request', { target_character_id: misty.characterId }, 1), ash.ws);
  const req = await waitFor(misty.ws, 'misty', (m) => m.type === 'trade_request_received');
  const sessionId = req.payload.trade_session_id;

  send(envelope('trade_accept', { trade_session_id: sessionId }, 2), misty.ws);
  await new Promise((r) => setTimeout(r, 300));

  send(envelope('trade_offer_set', { trade_session_id: sessionId, pokemon_id: ashMon }, 3), ash.ws);
  await new Promise((r) => setTimeout(r, 300));

  const lockedLocation = psql(`SELECT location FROM pokemon WHERE id = '${ashMon}';`);
  console.log('location del pokemon de ash tras ofrecerlo:', lockedLocation);
  if (lockedLocation !== 'in_trade') throw new Error('FALLO: el pokemon no quedó bloqueado (in_trade) tras la oferta');

  const cancelOnMisty = waitFor(misty.ws, 'misty', (m) => m.type === 'trade_cancelled');
  console.log('ash se desconecta abruptamente (sin declinar ni cancelar)...');
  ash.ws.close();
  await cancelOnMisty;

  await new Promise((r) => setTimeout(r, 300)); // dar tiempo a que el defer del servidor corra
  const finalLocation = psql(`SELECT location FROM pokemon WHERE id = '${ashMon}';`);
  const finalStatus = psql(`SELECT status FROM trade_sessions WHERE id = '${sessionId}';`);
  console.log('location del pokemon tras la desconexión:', finalLocation);
  console.log('status de la sesión tras la desconexión:', finalStatus);

  if (finalLocation === 'in_trade') throw new Error('FALLO: el pokemon quedó bloqueado para siempre tras la desconexión');
  if (finalStatus !== 'cancelled') throw new Error('FALLO: la sesión no quedó cancelled tras la desconexión');
  console.log('OK: al desconectarse un jugador a mitad de trade, el otro fue notificado y el Pokémon se liberó.');

  misty.ws.close();
}

async function main() {
  await scenarioDecline();
  await scenarioDisconnect();
  console.log('\nTodos los escenarios de cancelación pasaron.');
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
