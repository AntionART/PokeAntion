import { WebSocket } from 'ws';
import { randomUUID } from 'node:crypto';

const HTTP = process.env.HTTP_HOST || 'http://localhost:8080';
const WS_URL = process.env.WS_URL || 'ws://localhost:8080/ws';

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
    ws.on('message', (data) => messages.push(JSON.parse(data.toString())));
    ws.on('open', () => send(envelope('login', { username, password }, 1), ws));
    ws.on('error', reject);
    const timer = setTimeout(() => reject(new Error('timeout en login')), 5000);
    const check = setInterval(() => {
      const ok = messages.find((m) => m.type === 'login_ok');
      if (ok) { clearInterval(check); clearTimeout(timer); resolve({ ws, messages, characterId: ok.payload.character_id, name: username }); }
    }, 50);
  });
}

function waitFor(session, predicate, timeoutMs = 5000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`timeout esperando mensaje en ${session.name}: ` + JSON.stringify(session.messages.slice(-5)))), timeoutMs);
    function handler(data) {
      const msg = JSON.parse(data.toString());
      if (predicate(msg)) { clearTimeout(timer); session.ws.off('message', handler); resolve(msg); }
    }
    session.ws.on('message', handler);
  });
}

function leaderOf(partyUpdate) {
  return partyUpdate.payload.members.find((m) => m.is_leader);
}

async function main() {
  const suffix = randomUUID().slice(0, 8);
  const names = ['leader', 'memberB', 'memberC'].map((n) => `${n}_${suffix}`);
  for (const n of names) await register(n, 'pass1234');

  const leader = await connectAndLogin(names[0], 'pass1234');
  const memberB = await connectAndLogin(names[1], 'pass1234');
  const memberC = await connectAndLogin(names[2], 'pass1234');

  console.log('=== 1. leader invita a memberB ===');
  send(envelope('party_invite', { target_character_id: memberB.characterId }, 1), leader.ws);
  const invite1 = await waitFor(memberB, (m) => m.type === 'party_invite_received');
  const partyId = invite1.payload.party_id;
  console.log('memberB recibió invitación al grupo', partyId, 'de', invite1.payload.from_nickname);

  console.log('\n=== 2. memberB acepta -> ambos reciben party_update con 2 miembros ===');
  const updLeaderP = waitFor(leader, (m) => m.type === 'party_update' && m.payload.members.length === 2);
  const updMemberBP = waitFor(memberB, (m) => m.type === 'party_update' && m.payload.members.length === 2);
  send(envelope('party_accept', { party_id: partyId }, 2), memberB.ws);
  const [updLeader, updMemberB] = await Promise.all([updLeaderP, updMemberBP]);
  console.log('party_update (leader):', updLeader.payload);
  const leaderEntry = leaderOf(updLeader);
  if (leaderEntry.character_id !== leader.characterId) throw new Error('FALLO: el líder no es quien creó el grupo');
  console.log('OK: grupo con 2 miembros, líder correcto.');

  console.log('\n=== 3. memberB (no-líder) intenta invitar -> debe fallar ===');
  send(envelope('party_invite', { target_character_id: memberC.characterId }, 3), memberB.ws);
  const forbidden = await waitFor(memberB, (m) => m.type === 'error');
  console.log('error esperado:', forbidden.payload);
  if (forbidden.payload.code !== 'invalid_state') throw new Error('FALLO: se esperaba invalid_state al invitar sin ser líder');

  console.log('\n=== 4. leader invita a memberC, memberC acepta -> 3 miembros ===');
  send(envelope('party_invite', { target_character_id: memberC.characterId }, 4), leader.ws);
  const invite2 = await waitFor(memberC, (m) => m.type === 'party_invite_received');
  const upd3P = waitFor(memberC, (m) => m.type === 'party_update' && m.payload.members.length === 3);
  send(envelope('party_accept', { party_id: invite2.payload.party_id }, 5), memberC.ws);
  const upd3 = await upd3P;
  console.log('party_update con 3 miembros:', upd3.payload.members.map((m) => `${m.nickname}${m.is_leader ? ' (líder)' : ''}`));
  if (upd3.payload.members.length !== 3) throw new Error('FALLO: el grupo debería tener 3 miembros');

  console.log('\n=== 5. el líder se va -> liderazgo pasa a memberB (el más antiguo restante) ===');
  const afterLeaveB = waitFor(memberB, (m) => m.type === 'party_update' && m.payload.members.length === 2);
  const afterLeaveC = waitFor(memberC, (m) => m.type === 'party_update' && m.payload.members.length === 2);
  send(envelope('party_leave', {}, 6), leader.ws);
  const [b2, c2] = await Promise.all([afterLeaveB, afterLeaveC]);
  const newLeader = leaderOf(b2);
  console.log('nuevo líder tras la salida:', newLeader);
  if (newLeader.character_id !== memberB.characterId) throw new Error('FALLO: el liderazgo no pasó a memberB');
  console.log('OK: liderazgo transferido correctamente, ambos miembros restantes vieron el update.');

  console.log('\n=== 6. memberB se va -> memberC queda solo (grupo de 1, sigue líder) ===');
  const afterLeaveC2 = waitFor(memberC, (m) => m.type === 'party_update' && m.payload.members.length === 1);
  send(envelope('party_leave', {}, 7), memberB.ws);
  const c3 = await afterLeaveC2;
  if (c3.payload.members[0].character_id !== memberC.characterId || !c3.payload.members[0].is_leader) {
    throw new Error('FALLO: memberC debería quedar como único miembro y líder');
  }
  console.log('OK: memberC quedó solo y como líder.');

  console.log('\n=== 7. memberC se va -> grupo disuelto (party_disbanded) ===');
  const disbandP = waitFor(memberC, (m) => m.type === 'party_disbanded');
  send(envelope('party_leave', {}, 8), memberC.ws);
  const disband = await disbandP;
  console.log('party_disbanded:', disband.payload);
  if (disband.payload.party_id !== partyId) throw new Error('FALLO: party_disbanded con party_id incorrecto');
  console.log('OK: el grupo se disolvió al quedar vacío.');

  console.log('\nTodos los escenarios de grupos pasaron.');
  leader.ws.close(); memberB.ws.close(); memberC.ws.close();
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
