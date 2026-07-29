// Smoke test real de encuentros salvajes: UNA conexión WebSocket real (no hace falta un
// segundo jugador, a diferencia de battle-smoke.js) reporta estar en Route 101, dispara
// wild_encounter_triggered repetidas veces hasta que un encuentro real prenda, pelea hasta
// atraparlo con una Master Ball, y confirma en la base que la fila nueva existe.
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
    body: JSON.stringify({ username, email: `${username}@example.com`, password, rom_id: 'emerald_es', nickname: 'WildTest', starter_species: 280 })
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
      console.log('<-', msg.type, JSON.stringify(msg.payload));
      if (predicate(msg)) { clearTimeout(timer); ws.off('message', handler); resolve(msg); }
    }
    const timer = setTimeout(() => { ws.off('message', handler); reject(new Error('timeout esperando mensaje')); }, timeoutMs);
    ws.on('message', handler);
  });
}

async function main() {
  const username = `wildtest_${randomUUID().slice(0, 8)}`;
  console.log('=== 1. Registrar cuenta con Torchic inicial ===');
  const reg = await register(username, 'wildpass123');
  console.log('character_id:', reg.character_id, 'starter_species:', reg.starter_species);

  const ws = new WebSocket(WS_URL);
  await new Promise((resolve, reject) => { ws.on('open', resolve); ws.on('error', reject); });
  send(envelope('login', { username, password: 'wildpass123' }, 1), ws);
  await waitFor(ws, (m) => m.type === 'login_ok');

  console.log('\n=== 2. Reportar posición en Route 101 (map_id real, no un pueblo) ===');
  send(envelope('move', { map_id: 'MAP_ROUTE101', x: 10, y: 10, facing: 'down', state: 'walking' }, 2), ws);
  await new Promise((r) => setTimeout(r, 200)); // darle tiempo al Hub de registrar el mapa

  console.log('\n=== 3. Disparar encuentros hasta que uno prenda (¿~11% por intento en Route 101) ===');
  let started = null;
  for (let i = 0; i < 60 && !started; i++) {
    send(envelope('wild_encounter_triggered', { map_id: 'MAP_ROUTE101' }, 10 + i), ws);
    try {
      started = await waitFor(ws, (m) => m.type === 'wild_battle_start', 300);
    } catch { /* no prendió esta vez, seguir intentando */ }
  }
  if (!started) throw new Error('no se disparó ningún encuentro salvaje en 60 intentos');
  const sessionId = started.payload.session_id;
  console.log('wild_battle_start:', started.payload);

  console.log('\n=== 4. Pelear con Master Ball hasta atrapar ===');
  const MASTER_BALL = 1;
  psql(`INSERT INTO inventory_items (owner_char_id, item_id, quantity, pocket) VALUES ('${reg.character_id}', ${MASTER_BALL}, 1, 'balls')
        ON CONFLICT (owner_char_id, item_id) DO UPDATE SET quantity = 1;`);
  send(envelope('wild_throw_ball', { session_id: sessionId, item_id: MASTER_BALL }, 100), ws);
  const end = await waitFor(ws, (m) => m.type === 'wild_battle_end');
  console.log('wild_battle_end:', end.payload);

  if (end.payload.reason !== 'caught' || !end.payload.caught_pokemon) {
    console.error('\nFALLO: la Master Ball debería atrapar siempre.');
    process.exit(1);
  }

  console.log('\n=== 5. Verificar contra la base de datos ===');
  const newMonId = end.payload.caught_pokemon.id;
  const owner = psql(`SELECT owner_char_id FROM pokemon WHERE id = '${newMonId}';`);
  const caught = psql(`SELECT caught FROM pokedex_entries WHERE owner_char_id = '${reg.character_id}' AND species_id = ${end.payload.caught_pokemon.species_id};`);
  console.log(`dueño del pokémon atrapado: ${owner} (esperado: ${reg.character_id})`);
  console.log(`pokedex_entries.caught: ${caught}`);

  ws.close();
  if (owner === reg.character_id && caught === 't') {
    console.log('\n=== TODO OK: encuentro salvaje real (1 conexión WS) verificado de punta a punta contra el servidor real. ===');
  } else {
    console.error('\nFALLO en la verificación de base de datos.');
    process.exit(1);
  }
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
