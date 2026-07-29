// Smoke test real de batalla PvP: DOS conexiones WebSocket independientes (no un solo cliente
// con inyección sintética como --debug-battle) completan challenge -> accept -> N turnos de
// pelea -> battle_end, y por separado un flujo de huida — el gap explícito que quedó sin cubrir
// después de la sesión que construyó todo el sistema de batalla (ver memoria del proyecto).
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

function sqlJson(obj) {
  return JSON.stringify(obj).replace(/'/g, "''");
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

// waitForAll: como waitFor pero sigue escuchando y resuelve con TODOS los mensajes que cumplan
// el predicado hasta que uno de ellos también cumpla stopPredicate (o el timeout) — usado para
// juntar la secuencia de battle_turn_result de un lado hasta que llega su battle_end.
function waitForAll(ws, name, predicate, stopPredicate, timeoutMs = 15000) {
  return new Promise((resolve, reject) => {
    const collected = [];
    const timer = setTimeout(() => reject(new Error(`timeout esperando secuencia en ${name}`)), timeoutMs);
    function handler(data) {
      const msg = JSON.parse(data.toString());
      if (predicate(msg)) {
        console.log(`${name} <-`, msg.type, JSON.stringify(msg.payload));
        collected.push(msg);
        if (stopPredicate(msg)) {
          clearTimeout(timer);
          ws.off('message', handler);
          resolve(collected);
        }
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

function insertActiveMon(characterId, speciesId, hp, attack, defense, spAttack, spDefense, speed, moveIds) {
  const id = randomUUID();
  const moves = sqlJson(moveIds.map((m) => ({ move_id: m, pp_current: 35, pp_max: 35 })));
  const cols = `(id, owner_char_id, species_id, level, personality, ot_id, hp_current, hp_max,
                 stat_attack, stat_defense, stat_sp_attack, stat_sp_defense, stat_speed,
                 nature, moves, location, team_slot)`;
  psql(`INSERT INTO pokemon ${cols} VALUES (
    '${id}', '${characterId}', ${speciesId}, 5, 12345, 67890, ${hp}, ${hp},
    ${attack}, ${defense}, ${spAttack}, ${spDefense}, ${speed},
    1, '${moves}'::jsonb, 'team', 0
  );`);
  return id;
}

async function main() {
  const suffix = randomUUID().slice(0, 8);
  const ashUser = `battleash_${suffix}`, mistyUser = `battlemisty_${suffix}`;

  console.log('=== 1. Registrar dos cuentas nuevas ===');
  const ashReg = await register(ashUser, `${ashUser}@example.com`, 'pikachu123', 'AshBattle');
  const mistyReg = await register(mistyUser, `${mistyUser}@example.com`, 'squirtle123', 'MistyBattle');
  if (!ashReg.character_id || !mistyReg.character_id) {
    throw new Error('el registro no devolvió character_id');
  }
  console.log('ash character_id:', ashReg.character_id);
  console.log('misty character_id:', mistyReg.character_id);

  console.log('\n=== 2. Pokémon activo real para cada uno (mismos datos que battle.TestSimulatedBattle) ===');
  const ashMonId = insertActiveMon(ashReg.character_id, 280, 19, 15, 9, 16, 10, 9, [10, 45]); // Torchic: Scratch, Growl
  const mistyMonId = insertActiveMon(mistyReg.character_id, 283, 20, 17, 12, 12, 12, 7, [33, 45]); // Mudkip: Tackle, Growl
  console.log('ash pokemon (Torchic):', ashMonId);
  console.log('misty pokemon (Mudkip):', mistyMonId);

  console.log('\n=== 3. Login por WebSocket de ambos ===');
  const ash = await connectAndLogin(ashUser, 'pikachu123');
  const misty = await connectAndLogin(mistyUser, 'squirtle123');

  console.log('\n=== 4. Desafío: challenge -> accept -> battle_start (dos conexiones reales) ===');
  send(envelope('battle_challenge', { target_character_id: misty.characterId }, 30), ash.ws);
  const challengeMsg = await waitFor(misty.ws, 'misty', (m) => m.type === 'battle_challenge_received');
  const sessionId = challengeMsg.payload.battle_session_id;
  console.log('battle_session_id:', sessionId);

  const startAshP = waitFor(ash.ws, 'ash', (m) => m.type === 'battle_start');
  const startMistyP = waitFor(misty.ws, 'misty', (m) => m.type === 'battle_start');
  send(envelope('battle_accept', { battle_session_id: sessionId }, 31), misty.ws);
  const [startAsh, startMisty] = await Promise.all([startAshP, startMistyP]);

  if (startAsh.payload.yours.species_id !== 280 || startAsh.payload.opponent.species_id !== 283) {
    throw new Error('battle_start de ash no tiene las especies esperadas (perspectiva yours/opponent invertida?)');
  }
  if (startMisty.payload.yours.species_id !== 283 || startMisty.payload.opponent.species_id !== 280) {
    throw new Error('battle_start de misty no tiene las especies esperadas');
  }
  console.log('OK: cada lado ve su propia perspectiva (yours/opponent) correctamente.');

  console.log('\n=== 5. Pelear hasta el final: ambos mandan battle_action en cada intercambio ===');
  const endAshP = waitForAll(ash.ws, 'ash',
    (m) => m.type === 'battle_turn_result' || m.type === 'battle_end',
    (m) => m.type === 'battle_end');
  const endMistyP = waitForAll(misty.ws, 'misty',
    (m) => m.type === 'battle_turn_result' || m.type === 'battle_end',
    (m) => m.type === 'battle_end');

  // Turno 0 inicial (ninguno esperando resultado todavía) + hasta 30 intercambios más si hace
  // falta: como no sabemos de antemano cuántos turnos toma (daño real con variación aleatoria),
  // mandamos una ráfaga generosa; el servidor ignora acciones sobrantes una vez que la sesión
  // ya terminó (SubmitAction devuelve ErrSessionNotFound, no rompe nada del lado del bot).
  for (let i = 0; i < 30; i++) {
    send(envelope('battle_action', { battle_session_id: sessionId, move_slot: 0 }, 40 + i), ash.ws);
    send(envelope('battle_action', { battle_session_id: sessionId, move_slot: 0 }, 40 + i), misty.ws);
    await new Promise((r) => setTimeout(r, 120));
  }

  const [ashMsgs, mistyMsgs] = await Promise.all([endAshP, endMistyP]);
  const ashEnd = ashMsgs[ashMsgs.length - 1].payload;
  const mistyEnd = mistyMsgs[mistyMsgs.length - 1].payload;
  console.log('battle_end (ash):', ashEnd);
  console.log('battle_end (misty):', mistyEnd);

  const winner = ashEnd.winner_character_id;
  const validWinner = winner === ash.characterId || winner === misty.characterId;
  const consistentYouWon = ashEnd.you_won === (winner === ash.characterId) && mistyEnd.you_won === (winner === misty.characterId);
  const bothVictory = ashEnd.reason === 'victory' && mistyEnd.reason === 'victory';

  console.log('\n=== 6. Verificar contra la base de datos ===');
  const winnerMonId = winner === ash.characterId ? ashMonId : mistyMonId;
  const loserMonId = winner === ash.characterId ? mistyMonId : ashMonId;
  const winnerHp = psql(`SELECT hp_current FROM pokemon WHERE id = '${winnerMonId}';`);
  const loserHp = psql(`SELECT hp_current FROM pokemon WHERE id = '${loserMonId}';`);
  console.log(`HP del ganador (${winnerMonId}): ${winnerHp}`);
  console.log(`HP del perdedor (${loserMonId}): ${loserHp}`);

  const dbOk = Number(winnerHp) > 0 && Number(loserHp) === 0;

  let ok1 = validWinner && consistentYouWon && bothVictory && dbOk;
  if (!ok1) {
    console.error('\nFALLO en el escenario de pelea hasta el final.');
    process.exitCode = 1;
  } else {
    console.log('\nOK: la batalla completa (2 conexiones WS reales, sin GUI) terminó con un ganador válido,');
    console.log('ambos lados de acuerdo en quién ganó, y el HP persistido en la base de datos coincide.');
  }

  console.log('\n=== 7. Segundo escenario, sesión nueva: Huir termina la batalla de inmediato ===');
  console.log('(el Torchic de ash quedó en 0 HP del escenario anterior — curarlo antes de desafiar de nuevo, si no Accept falla con "no tenés ningún Pokémon en condiciones de pelear", correctamente)');
  psql(`UPDATE pokemon SET hp_current = hp_max WHERE id IN ('${ashMonId}', '${mistyMonId}');`);

  send(envelope('battle_challenge', { target_character_id: misty.characterId }, 90), ash.ws);
  const challenge2 = await waitFor(misty.ws, 'misty', (m) => m.type === 'battle_challenge_received');
  const sessionId2 = challenge2.payload.battle_session_id;

  const start2AshP = waitFor(ash.ws, 'ash', (m) => m.type === 'battle_start');
  send(envelope('battle_accept', { battle_session_id: sessionId2 }, 91), misty.ws);
  await start2AshP;

  const fleeAshP = waitFor(ash.ws, 'ash', (m) => m.type === 'battle_end');
  const fleeMistyP = waitFor(misty.ws, 'misty', (m) => m.type === 'battle_end');
  send(envelope('battle_flee', { battle_session_id: sessionId2 }, 92), ash.ws);
  const [fleeAsh, fleeMisty] = await Promise.all([fleeAshP, fleeMistyP]);
  console.log('battle_end tras huir (ash, el que huyó):', fleeAsh.payload);
  console.log('battle_end tras huir (misty):', fleeMisty.payload);

  const ok2 = fleeAsh.payload.reason === 'fled' && fleeMisty.payload.reason === 'fled' &&
    fleeAsh.payload.you_won === false && fleeMisty.payload.you_won === true &&
    fleeAsh.payload.winner_character_id === misty.characterId;
  if (!ok2) {
    console.error('\nFALLO en el escenario de huida.');
    process.exitCode = 1;
  } else {
    console.log('\nOK: huir termina la batalla de inmediato a favor del rival, sin esperar su jugada.');
  }

  ash.ws.close(); misty.ws.close();

  if (ok1 && ok2) {
    console.log('\n=== TODO OK: sistema de batalla PvP verificado con dos conexiones WebSocket reales e independientes. ===');
  } else {
    process.exit(1);
  }
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
