import { WebSocket } from 'ws';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';

// Verifica las adiciones al protocolo pensadas para que un cliente real (sin acceso directo
// a la base de datos) pueda armar una UI de trade utilizable: list_my_pokemon, y las
// notificaciones que antes faltaban (trade_accepted, trade_offer_updated, nickname en
// trade_request_received). Sin esto, un jugador real no tiene forma de saber qué Pokémon
// tiene para ofrecer, ni de ver la oferta del otro antes de confirmar "a ciegas".

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
    body: JSON.stringify({ username, email: `${username}@example.com`, password, rom_id: 'emerald_es', nickname: username })
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
  const aUser = `tuia_${suffix}`, bUser = `tuib_${suffix}`;

  console.log('=== 1. Registrar dos cuentas y darles un Pokémon cada una ===');
  const aReg = await register(aUser, 'password123');
  const bReg = await register(bUser, 'password123');
  const aMonId = randomUUID();
  const bMonId = randomUUID();
  const cols = `(id, owner_char_id, species_id, nickname, level, hp_current, hp_max, stat_attack, stat_defense, stat_sp_attack, stat_sp_defense, stat_speed, nature, location, team_slot)`;
  psql(`INSERT INTO pokemon ${cols} VALUES ('${aMonId}', '${aReg.character_id}', 25, 'Sparky', 5, 20, 20, 10, 10, 10, 10, 10, 1, 'team', 0);`);
  psql(`INSERT INTO pokemon ${cols} VALUES ('${bMonId}', '${bReg.character_id}', 7, NULL, 5, 20, 20, 10, 10, 10, 10, 10, 1, 'team', 0);`);

  const a = await connectAndLogin(aUser, 'password123');
  const b = await connectAndLogin(bUser, 'password123');

  console.log('\n=== 2. list_my_pokemon: A debe ver su Pikachu "Sparky" ===');
  send(envelope('list_my_pokemon', {}), a.ws);
  const aList = await waitFor(a.ws, 'a', (m) => m.type === 'my_pokemon_list');
  console.log('my_pokemon_list de A:', JSON.stringify(aList.payload));
  const found = aList.payload.pokemon.find((p) => p.id === aMonId);
  if (!found) throw new Error('FALLO: list_my_pokemon no devolvió el Pokémon esperado');
  if (found.species_id !== 25 || found.nickname !== 'Sparky' || found.level !== 5) {
    throw new Error(`FALLO: datos del pokémon incorrectos: ${JSON.stringify(found)}`);
  }
  console.log('OK: list_my_pokemon devuelve species_id/nickname/level correctos.');

  console.log('\n=== 3. trade_request_received debe incluir el nickname del emisor ===');
  send(envelope('trade_request', { target_character_id: b.characterId }), a.ws);
  const reqMsg = await waitFor(b.ws, 'b', (m) => m.type === 'trade_request_received');
  console.log('trade_request_received:', JSON.stringify(reqMsg.payload));
  if (reqMsg.payload.from_nickname !== aUser) {
    throw new Error(`FALLO: from_nickname esperado "${aUser}", llegó "${reqMsg.payload.from_nickname}"`);
  }
  console.log('OK: trade_request_received trae from_nickname.');
  const sessionId = reqMsg.payload.trade_session_id;

  console.log('\n=== 4. trade_accept debe notificar a AMBOS con trade_accepted ===');
  const acceptedAP = waitFor(a.ws, 'a', (m) => m.type === 'trade_accepted');
  const acceptedBP = waitFor(b.ws, 'b', (m) => m.type === 'trade_accepted');
  send(envelope('trade_accept', { trade_session_id: sessionId }), b.ws);
  const [acceptedA, acceptedB] = await Promise.all([acceptedAP, acceptedBP]);
  if (acceptedA.payload.trade_session_id !== sessionId || acceptedB.payload.trade_session_id !== sessionId) {
    throw new Error('FALLO: trade_accepted con session_id incorrecto');
  }
  console.log('OK: ambos participantes recibieron trade_accepted (antes: nadie se enteraba).');

  console.log('\n=== 5. trade_offer_set debe notificar a AMBOS con trade_offer_updated (con datos del pokémon) ===');
  const offerSeenByBP = waitFor(b.ws, 'b', (m) => m.type === 'trade_offer_updated' && m.payload.character_id === a.characterId);
  const offerSeenByAP = waitFor(a.ws, 'a', (m) => m.type === 'trade_offer_updated' && m.payload.character_id === a.characterId);
  send(envelope('trade_offer_set', { trade_session_id: sessionId, pokemon_id: aMonId }), a.ws);
  const [offerSeenByB, offerSeenByA] = await Promise.all([offerSeenByBP, offerSeenByAP]);
  console.log('B ve la oferta de A:', JSON.stringify(offerSeenByB.payload));
  if (offerSeenByB.payload.pokemon.species_id !== 25 || offerSeenByB.payload.pokemon.nickname !== 'Sparky') {
    throw new Error('FALLO: B no ve los datos correctos del pokémon ofrecido por A');
  }
  if (offerSeenByA.payload.pokemon.id !== aMonId) {
    throw new Error('FALLO: A (el propio emisor) tampoco recibió la confirmación de su oferta');
  }
  console.log('OK: ambos ven en vivo qué se ofreció, con species/nickname/level — no a ciegas.');

  console.log('\n=== 6. Completar el trade normalmente (offer de B + confirm de ambos) ===');
  send(envelope('trade_offer_set', { trade_session_id: sessionId, pokemon_id: bMonId }), b.ws);
  await new Promise((r) => setTimeout(r, 300));
  const doneAP = waitFor(a.ws, 'a', (m) => m.type === 'trade_completed');
  const doneBP = waitFor(b.ws, 'b', (m) => m.type === 'trade_completed');
  send(envelope('trade_confirm', { trade_session_id: sessionId }), a.ws);
  send(envelope('trade_confirm', { trade_session_id: sessionId }), b.ws);
  await Promise.all([doneAP, doneBP]);
  console.log('OK: el trade se completó con normalidad después de los pasos nuevos.');

  a.ws.close(); b.ws.close();
  console.log('\nTodos los escenarios del protocolo de UI de trade pasaron.');
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
