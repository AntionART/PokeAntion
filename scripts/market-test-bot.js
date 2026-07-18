import { WebSocket } from 'ws';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { writeFileSync } from 'node:fs';

const HTTP = 'http://localhost:8080';
const WS_URL = 'ws://localhost:8080/ws';
const PSQL = 'C:/Users/AuxSistemas/Pictures/Antion/pokemon-online/postgresql-16.5/pgsql/bin/psql.exe';
const PG_ARGS_BASE = ['-U', 'pokemon', '-h', 'localhost', '-p', '5432', '-d', 'pokemon_online', '-t', '-A'];

function psql(sql) {
  return execFileSync(PSQL, [...PG_ARGS_BASE, '-c', sql], { env: { ...process.env, PGPASSWORD: 'pokemon' } }).toString().trim();
}

async function register(username, password) {
  await fetch(`${HTTP}/register`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, email: `${username}@example.com`, password, rom_id: 'emerald_es', nickname: username })
  });
}

async function main() {
  const suffix = randomUUID().slice(0, 6);
  const username = `marketbot_${suffix}`;
  const password = 'botpass123';
  await register(username, password);

  const ws = new WebSocket(WS_URL);
  ws.on('open', () => ws.send(JSON.stringify({ type: 'login', seq: 1, payload: { username, password } })));

  ws.on('message', (data) => {
    const msg = JSON.parse(data.toString());
    if (msg.type === 'login_ok') {
      const charId = msg.payload.character_id;
      console.log(`[bot] logueado: ${username} character_id=${charId}`);
      const monId = randomUUID();
      const cols = `(id, owner_char_id, species_id, nickname, level, hp_current, hp_max, stat_attack, stat_defense, stat_sp_attack, stat_sp_defense, stat_speed, nature, location, team_slot)`;
      psql(`INSERT INTO pokemon ${cols} VALUES ('${monId}', '${charId}', 4, 'Charmi', 7, 24, 24, 11, 10, 11, 10, 13, 5, 'team', 0);`);
      console.log(`[bot] pokemon creado: ${monId} (species 4, Charmander, "Charmi")`);
      writeFileSync('market-test-bot-info.json', JSON.stringify({ username, characterId: charId, pokemonId: monId }, null, 2));
      ws.send(JSON.stringify({ type: 'market_list', seq: 2, payload: { pokemon_id: monId, price: 777 } }));
    }
    if (msg.type === 'market_list_ok') {
      console.log(`[bot] publicado en el mercado: listing_id=${msg.payload.listing_id} precio=777`);
    }
    if (msg.type === 'market_sold') {
      console.log(`[bot] VENDIDO: ${msg.payload.buyer_nickname} pagó ${msg.payload.price}`);
    }
  });

  console.log('[bot] escuchando. Ctrl+C para cortar.');
}

main().catch((e) => { console.error('FAILED:', e); process.exit(1); });
