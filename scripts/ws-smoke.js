import { WebSocket } from 'ws';

const HTTP = process.env.HTTP_HOST || 'http://localhost:8080';
const WS_URL = process.env.WS_URL || 'ws://localhost:8080/ws';

async function register(username, email, password, nickname) {
  const res = await fetch(`${HTTP}/register`, {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({username, email, password, rom_id: 'emerald_us', nickname})
  });
  return await res.json();
}

function send(env, ws) {
  ws.send(JSON.stringify(env));
}

function envelope(type, payload, seq=0) {
  return { type, seq, payload };
}

async function main() {
  console.log('Smoke test: register two users (ash,misty), connect via WS and exercise move/chat/trade_request/accept');

  console.log('Registering users...');
  await register('ash', 'ash@example.com', 'pikachu123', 'Ash').catch(e=>console.error('reg ash err',e));
  await register('misty', 'misty@example.com', 'squirtle123', 'Misty').catch(e=>console.error('reg misty err',e));

  console.log('Connecting websockets...');
  const ws1 = new WebSocket(WS_URL);
  const ws2 = new WebSocket(WS_URL);

  let char1 = null, char2 = null;

  function setup(ws, name) {
    ws.on('open', () => {
      console.log(`${name} ws open`);
      // send login as first message
      send(envelope('login', { username: name, password: name==='ash' ? 'pikachu123' : 'squirtle123' }, 1), ws);
    });
    ws.on('message', (data) => {
      try {
        const msg = JSON.parse(data.toString());
        console.log(`${name} <-`, msg.type, msg.payload);
        if (msg.type === 'login_ok') {
          if (name === 'ash') char1 = msg.payload.character_id || msg.payload.character_id || msg.payload.CharacterID || msg.payload.character_id;
          if (name === 'misty') char2 = msg.payload.character_id || msg.payload.CharacterID || msg.payload.character_id;
        }
        if (msg.type === 'player_update') {
          console.log(`${name} received player_update from ${msg.payload.character_id || msg.payload.CharacterID}`);
        }
        if (msg.type === 'chat_message') {
          console.log(`${name} chat:`, msg.payload.from_nickname || msg.payload.fromNickname, msg.payload.message || msg.payload);
        }
        if (msg.type === 'trade_request_received') {
          console.log(`${name} got trade_request`, msg.payload);
          // auto-accept
          send(envelope('trade_accept', { trade_session_id: msg.payload.trade_session_id }), ws);
        }
        if (msg.type === 'trade_completed') {
          console.log(`${name} trade completed:`, msg.payload);
        }
      } catch (err) {
        console.error(name, 'invalid message', err);
      }
    });
    ws.on('close', () => console.log(`${name} ws closed`));
    ws.on('error', (e) => console.error(`${name} ws error`, e));
  }

  setup(ws1, 'ash');
  setup(ws2, 'misty');

  // helper to wait for login_ok
  await new Promise((resolve) => setTimeout(resolve, 1200));

  // ensure both logged in
  console.log('Waiting for login_ok... (2s)');
  await new Promise((resolve) => setTimeout(resolve, 2000));

  // ash moves
  console.log('ash moves to x=11 y=12');
  send(envelope('move', { map_id: 'littleroot_town', x: 11, y: 12, facing: 'up', state: 'walking' }, 10), ws1);

  // ash sends chat local
  await new Promise((resolve) => setTimeout(resolve, 500));
  send(envelope('send_chat', { channel: 'local', message: 'hola desde ash' }, 11), ws1);

  // initiate trade: ash -> misty
  await new Promise((resolve) => setTimeout(resolve, 500));
  console.log('ash requests trade with misty');
  // need character IDs: try to read from login_ok reception above
  // if not present, attempt simple heuristic: server may echo username as character id in skeleton
  const targetId = char2 || 'misty';
  send(envelope('trade_request', { target_character_id: targetId }, 20), ws1);

  // wait a few seconds for accept flow
  await new Promise((resolve) => setTimeout(resolve, 4000));

  console.log('Closing sockets');
  ws1.close(); ws2.close();
}

main().catch(e=>{ console.error(e); process.exit(1); });
