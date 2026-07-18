import { WebSocket } from 'ws';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';

const HTTP = process.env.HTTP_HOST || 'http://localhost:8080';
const WS_URL = process.env.WS_URL || 'ws://localhost:8080/ws';

const PSQL = process.env.PSQL_PATH || 'C:/Users/AuxSistemas/Pictures/Antion/pokemon-online/postgresql-16.5/pgsql/bin/psql.exe';
const PG_ARGS_BASE = ['-U', 'pokemon', '-h', 'localhost', '-p', '5432', '-d', 'pokemon_online', '-t', '-A'];

function psql(sql) {
  const out = execFileSync(PSQL, [...PG_ARGS_BASE, '-c', sql], {
    env: { ...process.env, PGPASSWORD: 'pokemon' },
  });
  return out.toString().trim();
}

async function register(username, email, password, nickname) {
  const res = await fetch(`${HTTP}/register`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, email, password, rom_id: 'emerald_us', nickname })
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
      if (predicate(msg)) {
        clearTimeout(timer);
        ws.off('message', handler);
        resolve(msg);
      }
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
  const ashUser = `ash_${suffix}`, mistyUser = `misty_${suffix}`;

  console.log('=== 1. Registrar dos cuentas nuevas ===');
  const ashReg = await register(ashUser, `${ashUser}@example.com`, 'pikachu123', 'Ash');
  const mistyReg = await register(mistyUser, `${mistyUser}@example.com`, 'squirtle123', 'Misty');
  console.log('ash register ->', ashReg);
  console.log('misty register ->', mistyReg);
  if (!ashReg.character_id || !mistyReg.character_id) {
    throw new Error('el registro no devolvió character_id en snake_case; revisar json tags de AuthResult');
  }

  console.log('\n=== 2. Insertar un Pokémon inicial para cada uno (directo en DB, como si vinieran de la ROM) ===');
  const ashMonId = randomUUID();
  const mistyMonId = randomUUID();
  const monCols = `(id, owner_char_id, species_id, level, hp_current, hp_max, stat_attack, stat_defense, stat_sp_attack, stat_sp_defense, stat_speed, nature, location, team_slot)`;
  psql(`INSERT INTO pokemon ${monCols} VALUES ('${ashMonId}', '${ashReg.character_id}', 25, 5, 20, 20, 10, 10, 10, 10, 10, 1, 'team', 0);`);
  psql(`INSERT INTO pokemon ${monCols} VALUES ('${mistyMonId}', '${mistyReg.character_id}', 7, 5, 20, 20, 10, 10, 10, 10, 10, 1, 'team', 0);`);
  console.log(`ash's pokemon: ${ashMonId} (species 25, Pikachu)`);
  console.log(`misty's pokemon: ${mistyMonId} (species 7, Squirtle)`);

  console.log('\n=== 3. Login por WebSocket de ambos ===');
  const ash = await connectAndLogin(ashUser, 'pikachu123');
  const misty = await connectAndLogin(mistyUser, 'squirtle123');
  console.log('ash characterId:', ash.characterId);
  console.log('misty characterId:', misty.characterId);

  console.log('\n=== 4. Trade: request -> accept -> offer (ambos) -> confirm (ambos) ===');
  send(envelope('trade_request', { target_character_id: misty.characterId }, 20), ash.ws);
  const reqMsg = await waitFor(misty.ws, 'misty', (m) => m.type === 'trade_request_received');
  const sessionId = reqMsg.payload.trade_session_id;
  console.log('trade_session_id:', sessionId);

  send(envelope('trade_accept', { trade_session_id: sessionId }, 21), misty.ws);
  await new Promise((r) => setTimeout(r, 300));

  send(envelope('trade_offer_set', { trade_session_id: sessionId, pokemon_id: ashMonId }, 22), ash.ws);
  send(envelope('trade_offer_set', { trade_session_id: sessionId, pokemon_id: mistyMonId }, 22), misty.ws);
  await new Promise((r) => setTimeout(r, 300));

  const doneAshP = waitFor(ash.ws, 'ash', (m) => m.type === 'trade_completed');
  const doneMistyP = waitFor(misty.ws, 'misty', (m) => m.type === 'trade_completed');
  send(envelope('trade_confirm', { trade_session_id: sessionId }, 23), ash.ws);
  send(envelope('trade_confirm', { trade_session_id: sessionId }, 23), misty.ws);

  const [doneAsh, doneMisty] = await Promise.all([doneAshP, doneMistyP]);
  console.log('ash trade_completed payload:', doneAsh.payload);
  console.log('misty trade_completed payload:', doneMisty.payload);

  console.log('\n=== 5. Verificar CONTRA LA BASE DE DATOS que el dueño realmente cambió ===');
  const ashMonOwnerNow = psql(`SELECT owner_char_id FROM pokemon WHERE id = '${ashMonId}';`);
  const mistyMonOwnerNow = psql(`SELECT owner_char_id FROM pokemon WHERE id = '${mistyMonId}';`);
  console.log(`pokemon que era de ash (${ashMonId}) ahora pertenece a: ${ashMonOwnerNow}`);
  console.log(`pokemon que era de misty (${mistyMonId}) ahora pertenece a: ${mistyMonOwnerNow}`);

  const sessionStatus = psql(`SELECT status FROM trade_sessions WHERE id = '${sessionId}';`);
  console.log(`trade_sessions.status: ${sessionStatus}`);

  const ok = ashMonOwnerNow === misty.characterId && mistyMonOwnerNow === ash.characterId && sessionStatus === 'completed';
  if (!ok) {
    console.error('\nFALLO: el intercambio de dueños en la base de datos no coincide con lo esperado.');
    process.exitCode = 1;
  } else {
    console.log('\nOK: el trade movió ambos Pokémon al nuevo dueño correctamente en la base de datos, sin duplicación.');
    const totalMons = psql(`SELECT count(*) FROM pokemon WHERE id IN ('${ashMonId}', '${mistyMonId}');`);
    console.log(`(verificación anti-duplicación: siguen existiendo exactamente 2 filas: ${totalMons})`);
  }

  ash.ws.close(); misty.ws.close();
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
