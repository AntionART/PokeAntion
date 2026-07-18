import { WebSocket } from 'ws';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { writeFileSync } from 'node:fs';

// Bot de un solo jugador ("B") para probar en vivo el SocialPanel del cliente real ("A"),
// que un humano/agente maneja con el teclado. B se registra, se loguea, se queda escuchando,
// y responde automáticamente a solicitudes de amistad/grupo/trade tal como lo haría un
// segundo jugador real aceptando desde su propio cliente.

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

async function main() {
  const suffix = randomUUID().slice(0, 6);
  const username = `bot_${suffix}`;
  const password = 'botpass123';
  const reg = await register(username, password);
  console.log(`[bot] registrado: ${username} character_id=${reg.character_id}`);

  const monId = randomUUID();
  const cols = `(id, owner_char_id, species_id, nickname, level, hp_current, hp_max, stat_attack, stat_defense, stat_sp_attack, stat_sp_defense, stat_speed, nature, location, team_slot)`;
  psql(`INSERT INTO pokemon ${cols} VALUES ('${monId}', '${reg.character_id}', 7, 'BotSquirtle', 8, 22, 22, 11, 12, 11, 12, 9, 2, 'team', 0);`);
  console.log(`[bot] pokemon asignado: ${monId} (species 7, Squirtle, "BotSquirtle")`);

  writeFileSync('social-panel-bot-info.json', JSON.stringify({ username, characterId: reg.character_id, pokemonId: monId }, null, 2));

  const ws = new WebSocket(WS_URL);
  ws.on('open', () => send(envelope('login', { username, password }, 1), ws));
  ws.on('error', (e) => console.error('[bot] ws error:', e.message));

  let mySessionId = null;
  let myPokemon = [];

  ws.on('message', (data, isBinary) => {
    if (isBinary) return;
    const msg = JSON.parse(data.toString());
    if (msg.type !== 'login_ok') console.log('[bot] <-', msg.type, JSON.stringify(msg.payload));

    switch (msg.type) {
      case 'login_ok':
        console.log(`[bot] conectado y logueado, character_id=${msg.payload.character_id}, map=${msg.payload.map_id}`);
        if (process.env.BOT_COLOR) {
          console.log(`[bot] fijando color de sprite: ${process.env.BOT_COLOR}`);
          send(envelope('set_color', { color: process.env.BOT_COLOR }), ws);
        }
        break;
      case 'friend_request_received':
        console.log(`[bot] auto-aceptando solicitud de amistad de ${msg.payload.from_username}`);
        send(envelope('friend_accept', { target_account_id: msg.payload.from_account_id }), ws);
        break;
      case 'party_invite_received':
        console.log(`[bot] auto-aceptando invitación de grupo de ${msg.payload.from_nickname}`);
        send(envelope('party_accept', { party_id: msg.payload.party_id }), ws);
        break;
      case 'guild_invite_received':
        console.log(`[bot] auto-aceptando invitación de gremio de ${msg.payload.from_nickname} al gremio ${msg.payload.guild_name}`);
        send(envelope('guild_accept', { guild_id: msg.payload.guild_id }), ws);
        break;
      case 'trade_request_received':
        mySessionId = msg.payload.trade_session_id;
        console.log(`[bot] auto-aceptando trade de ${msg.payload.from_nickname}, session=${mySessionId}`);
        send(envelope('trade_accept', { trade_session_id: mySessionId }), ws);
        break;
      case 'trade_accepted':
        mySessionId = msg.payload.trade_session_id;
        console.log('[bot] trade aceptado, pidiendo mi lista de pokémon...');
        send(envelope('list_my_pokemon', {}), ws);
        break;
      case 'my_pokemon_list':
        myPokemon = msg.payload.pokemon;
        if (mySessionId && myPokemon.length > 0) {
          console.log(`[bot] ofreciendo ${myPokemon[0].nickname} (species ${myPokemon[0].species_id})`);
          send(envelope('trade_offer_set', { trade_session_id: mySessionId, pokemon_id: myPokemon[0].id }), ws);
        }
        break;
      case 'trade_offer_updated':
        if (msg.payload.character_id !== reg.character_id) {
          console.log(`[bot] el otro jugador ofreció: species ${msg.payload.pokemon.species_id} "${msg.payload.pokemon.nickname}" — confirmando trade`);
          send(envelope('trade_confirm', { trade_session_id: mySessionId }), ws);
        }
        break;
      case 'trade_completed':
        console.log('[bot] ¡trade completado desde el lado del bot!');
        break;
    }
  });

  console.log('[bot] escuchando. Ctrl+C para cortar.');
}

main().catch((e) => { console.error('BOT FAILED:', e); process.exit(1); });
