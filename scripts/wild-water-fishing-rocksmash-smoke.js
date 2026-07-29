// Smoke test real de encuentros de agua/pesca/rock-smash (ver server/internal/wildencounter):
// misma idea que wild-smoke.js (land_mons) pero ejercitando los 3 tipos nuevos contra el
// servidor real. MAP_ROUTE102 tiene water_mons + fishing_mons, MAP_ROUTE111 tiene
// rock_smash_mons (datos reales de wild_encounters.json, ver data/pokemon/encounters.json).
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

async function register(username) {
  const res = await fetch(`${HTTP}/register`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, email: `${username}@example.com`, password: 'wfpass123', rom_id: 'emerald_es', nickname: 'WFTest', starter_species: 280 })
  });
  if (!res.ok) throw new Error(`register falló: ${res.status} ${await res.text()}`);
  return await res.json();
}

function send(env, ws) { ws.send(JSON.stringify(env)); }
function envelope(type, payload, seq = 0) { return { type, seq, payload }; }

function waitFor(ws, predicate, timeoutMs = 5000) {
  return new Promise((resolve, reject) => {
    function handler(data) {
      const msg = JSON.parse(data.toString());
      if (predicate(msg)) { clearTimeout(timer); ws.off('message', handler); resolve(msg); }
    }
    const timer = setTimeout(() => { ws.off('message', handler); reject(new Error('timeout esperando mensaje')); }, timeoutMs);
    ws.on('message', handler);
  });
}

async function registerLoginAndMove(username, mapId) {
  const reg = await register(username);
  // Master Ball (item_id=1) para que triggerAndCatch pueda atrapar siempre sin depender de la
  // fórmula de captura real (ya probada aparte en wildencounter_test.go) — mismo criterio que
  // wild-smoke.js.
  psql(`INSERT INTO inventory_items (owner_char_id, item_id, quantity, pocket) VALUES ('${reg.character_id}', 1, 1, 'balls')
        ON CONFLICT (owner_char_id, item_id) DO UPDATE SET quantity = 1;`);
  const ws = new WebSocket(WS_URL);
  await new Promise((resolve, reject) => { ws.on('open', resolve); ws.on('error', reject); });
  send(envelope('login', { username, password: 'wfpass123' }, 1), ws);
  await waitFor(ws, (m) => m.type === 'login_ok');
  send(envelope('move', { map_id: mapId, x: 10, y: 10, facing: 'down', state: 'walking' }, 2), ws);
  await new Promise((r) => setTimeout(r, 200));
  return { ws, characterId: reg.character_id };
}

async function triggerAndCatch(ws, mapId, encounterType, rodTier, label) {
  // 400 intentos: hasta el caso de tasa más baja (agua en MAP_ROUTE102, ~2.2% por tirada) tiene
  // una probabilidad de fallo total despreciable en 400 intentos (~0.02%). Fishing no tiene
  // chequeo de tasa (ver TryFishingEncounter), así que ahí siempre prende en el primer intento.
  let started = null;
  for (let i = 0; i < 400 && !started; i++) {
    send(envelope('wild_encounter_triggered', { map_id: mapId, encounter_type: encounterType, rod_tier: rodTier }, 10 + i), ws);
    try {
      started = await waitFor(ws, (m) => m.type === 'wild_battle_start' || m.type === 'error', 150);
    } catch { /* no prendió esta vez */ }
    if (started && started.type === 'error') throw new Error(`${label}: error del servidor: ${JSON.stringify(started.payload)}`);
  }
  if (!started) throw new Error(`${label}: no se disparó ningún encuentro en 400 intentos`);
  console.log(`${label}: wild_battle_start ->`, started.payload.wild);

  const sessionId = started.payload.session_id;
  send(envelope('wild_throw_ball', { session_id: sessionId, item_id: 1 }, 200), ws); // Master Ball
  const end = await waitFor(ws, (m) => m.type === 'wild_battle_end');
  if (end.payload.reason !== 'caught') throw new Error(`${label}: esperaba reason=caught, dio ${end.payload.reason}`);
  console.log(`${label}: atrapado ->`, end.payload.caught_pokemon);
  return end.payload.caught_pokemon;
}

async function main() {
  const runId = randomUUID().slice(0, 6);

  console.log('=== 1. Encuentro de AGUA (surf) en MAP_ROUTE102 ===');
  const water = await registerLoginAndMove(`wf_w_${runId}`, 'MAP_ROUTE102');
  const waterCaught = await triggerAndCatch(water.ws, 'MAP_ROUTE102', 'water', '', 'AGUA');

  console.log('\n=== 2. Encuentro de PESCA (old_rod) en MAP_ROUTE102 ===');
  const fish = await registerLoginAndMove(`wf_f_${runId}`, 'MAP_ROUTE102');
  // El kit inicial YA da una Old Rod (ver server/cmd/server/main.go startingItemKit, agregado
  // justamente para poder probar pesca fácil) — hay que sacársela a mano para de verdad
  // ejercitar el caso "sin caña", si no esta cuenta recién creada ya tendría una.
  psql(`DELETE FROM inventory_items WHERE owner_char_id = '${fish.characterId}' AND item_id = 262;`);
  // Sin caña: debe rechazar con error antes de tirar nada. Se reintenta (no una sola vez): un
  // "wild_encounter_triggered" que llegue antes de que el Hub termine de procesar el "move"
  // anterior podría resolver el mapa viejo por una carrera de timing, no por un bug real.
  let rejected = null;
  for (let i = 0; i < 20 && !rejected; i++) {
    send(envelope('wild_encounter_triggered', { map_id: 'MAP_ROUTE102', encounter_type: 'fishing', rod_tier: 'old_rod' }, 5 + i), fish.ws);
    try {
      rejected = await waitFor(fish.ws, (m) => m.type === 'error' || m.type === 'wild_battle_start', 200);
    } catch { /* reintentar */ }
  }
  if (!rejected) throw new Error('nunca llegó ni un error ni un wild_battle_start tras 20 intentos sin caña');
  if (rejected.type === 'wild_battle_start') {
    throw new Error(`se disparó un encuentro de pesca SIN tener la caña — el chequeo de posesión no está funcionando: ${JSON.stringify(rejected.payload)}`);
  }
  console.log('sin caña, rechazo esperado:', rejected.payload);
  if (!rejected.payload.message || !rejected.payload.message.includes('caña')) {
    throw new Error(`esperaba un error mencionando la caña de pescar, dio: ${JSON.stringify(rejected.payload)}`);
  }
  // Dar la caña y reintentar.
  psql(`INSERT INTO inventory_items (owner_char_id, item_id, quantity, pocket) VALUES ('${fish.characterId}', 262, 1, 'key_items')
        ON CONFLICT (owner_char_id, item_id) DO UPDATE SET quantity = 1;`);
  const fishCaught = await triggerAndCatch(fish.ws, 'MAP_ROUTE102', 'fishing', 'old_rod', 'PESCA');
  if (fishCaught.species_id !== 129 && fishCaught.species_id !== 118) {
    throw new Error(`PESCA con old_rod atrapó species_id=${fishCaught.species_id}, esperaba 129 (Magikarp) o 118 (Goldeen)`);
  }

  console.log('\n=== 3. Encuentro de ROCK SMASH en MAP_ROUTE111 ===');
  const rock = await registerLoginAndMove(`wf_r_${runId}`, 'MAP_ROUTE111');
  const rockCaught = await triggerAndCatch(rock.ws, 'MAP_ROUTE111', 'rock_smash', '', 'ROCK SMASH');
  if (rockCaught.species_id !== 74) {
    throw new Error(`ROCK SMASH atrapó species_id=${rockCaught.species_id}, esperaba 74 (Geodude)`);
  }

  water.ws.close(); fish.ws.close(); rock.ws.close();
  console.log('\n=== TODO OK: agua, pesca (con chequeo de caña) y rock smash verificados de punta a punta contra el servidor real. ===');
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
