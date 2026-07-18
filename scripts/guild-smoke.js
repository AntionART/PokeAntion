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

function memberEntry(update, characterId) {
  return update.payload.members.find((m) => m.character_id === characterId);
}

async function main() {
  const suffix = randomUUID().slice(0, 8);
  const names = ['leader', 'memberB', 'memberC', 'outsider'].map((n) => `${n}_${suffix}`);
  for (const n of names) await register(n, 'pass1234');

  const leader = await connectAndLogin(names[0], 'pass1234');
  const memberB = await connectAndLogin(names[1], 'pass1234');
  const memberC = await connectAndLogin(names[2], 'pass1234');
  const outsider = await connectAndLogin(names[3], 'pass1234');

  const guildName = `Gremio_${suffix}`;

  console.log('=== 1. leader crea el gremio ===');
  const created1 = waitFor(leader, (m) => m.type === 'guild_update' && m.payload.members.length === 1);
  send(envelope('guild_create', { name: guildName }, 1), leader.ws);
  const created = await created1;
  const guildId = created.payload.guild_id;
  if (created.payload.name !== guildName) throw new Error('FALLO: nombre de gremio incorrecto');
  if (!created.payload.members[0].is_officer) throw new Error('FALLO: el fundador debería ser oficial/líder');
  console.log('OK: gremio creado', guildId, created.payload.name);

  console.log('\n=== 2. crear un segundo gremio con el mismo nombre debe fallar ===');
  send(envelope('guild_create', { name: guildName }, 2), memberB.ws);
  const nameTaken = await waitFor(memberB, (m) => m.type === 'error');
  console.log('error esperado:', nameTaken.payload);
  if (nameTaken.payload.code !== 'invalid_state') throw new Error('FALLO: se esperaba invalid_state por nombre duplicado');

  console.log('\n=== 3. leader invita a memberB ===');
  send(envelope('guild_invite', { target_character_id: memberB.characterId }, 3), leader.ws);
  const invite1 = await waitFor(memberB, (m) => m.type === 'guild_invite_received');
  if (invite1.payload.guild_id !== guildId) throw new Error('FALLO: guild_id incorrecto en la invitación');
  console.log('memberB recibió invitación de', invite1.payload.from_nickname, 'al gremio', invite1.payload.guild_name);

  console.log('\n=== 4. memberB acepta -> ambos reciben guild_update con 2 miembros ===');
  const updLeaderP = waitFor(leader, (m) => m.type === 'guild_update' && m.payload.members.length === 2);
  const updMemberBP = waitFor(memberB, (m) => m.type === 'guild_update' && m.payload.members.length === 2);
  send(envelope('guild_accept', { guild_id: guildId }, 4), memberB.ws);
  const [updLeader, updMemberB] = await Promise.all([updLeaderP, updMemberBP]);
  const bEntry = memberEntry(updLeader, memberB.characterId);
  if (!bEntry || bEntry.is_officer) throw new Error('FALLO: memberB debería estar en el gremio, sin ser oficial');
  console.log('OK: gremio con 2 miembros.', updLeader.payload.members.map((m) => `${m.nickname}${m.is_officer ? ' (líder)' : ''}`));

  console.log('\n=== 5. memberB (no-líder) intenta invitar -> debe fallar ===');
  send(envelope('guild_invite', { target_character_id: memberC.characterId }, 5), memberB.ws);
  const forbidden = await waitFor(memberB, (m) => m.type === 'error');
  console.log('error esperado:', forbidden.payload);
  if (forbidden.payload.code !== 'invalid_state') throw new Error('FALLO: se esperaba invalid_state al invitar sin ser líder');

  console.log('\n=== 6. leader invita a memberC, memberC declina ===');
  send(envelope('guild_invite', { target_character_id: memberC.characterId }, 6), leader.ws);
  const invite2 = await waitFor(memberC, (m) => m.type === 'guild_invite_received');
  send(envelope('guild_decline', { guild_id: invite2.payload.guild_id }, 7), memberC.ws);
  // no hay confirmación explícita de decline; verificamos indirectamente reintentando la invitación luego.
  await new Promise((r) => setTimeout(r, 200));
  console.log('OK: memberC declinó sin unirse.');

  console.log('\n=== 7. leader invita a memberC de nuevo, memberC acepta -> 3 miembros ===');
  send(envelope('guild_invite', { target_character_id: memberC.characterId }, 8), leader.ws);
  const invite3 = await waitFor(memberC, (m) => m.type === 'guild_invite_received');
  const upd3P = waitFor(memberC, (m) => m.type === 'guild_update' && m.payload.members.length === 3);
  send(envelope('guild_accept', { guild_id: invite3.payload.guild_id }, 9), memberC.ws);
  const upd3 = await upd3P;
  console.log('guild_update con 3 miembros:', upd3.payload.members.map((m) => `${m.nickname}${m.is_officer ? ' (líder)' : ''}`));
  if (upd3.payload.members.length !== 3) throw new Error('FALLO: el gremio debería tener 3 miembros');

  console.log('\n=== 8. canal de chat "guild": leader manda, memberB y memberC lo reciben, outsider NO ===');
  const chatB = waitFor(memberB, (m) => m.type === 'chat_message' && m.payload.channel === 'guild');
  const chatC = waitFor(memberC, (m) => m.type === 'chat_message' && m.payload.channel === 'guild');
  const chatLeaderEcho = waitFor(leader, (m) => m.type === 'chat_message' && m.payload.channel === 'guild');
  send(envelope('send_chat', { channel: 'guild', message: 'hola gremio' }, 10), leader.ws);
  const [gb, gc, gl] = await Promise.all([chatB, chatC, chatLeaderEcho]);
  if (gb.payload.message !== 'hola gremio' || gc.payload.message !== 'hola gremio' || gl.payload.message !== 'hola gremio') {
    throw new Error('FALLO: mensaje de chat de gremio incorrecto');
  }
  console.log('OK: chat de gremio llega a los 3 miembros (incluido el emisor, eco).');
  let outsiderGotIt = false;
  const outsiderHandler = (data) => { const m = JSON.parse(data.toString()); if (m.type === 'chat_message' && m.payload.channel === 'guild') outsiderGotIt = true; };
  outsider.ws.on('message', outsiderHandler);
  await new Promise((r) => setTimeout(r, 300));
  outsider.ws.off('message', outsiderHandler);
  if (outsiderGotIt) throw new Error('FALLO: outsider no debería recibir chat de gremio');
  console.log('OK: outsider no recibió el chat de gremio.');

  console.log('\n=== 9. leader expulsa a memberC ===');
  const kickedEnvP = waitFor(memberC, (m) => m.type === 'guild_disbanded' && m.payload.reason === 'kicked');
  const updAfterKickP = waitFor(memberB, (m) => m.type === 'guild_update' && m.payload.members.length === 2);
  send(envelope('guild_kick', { target_character_id: memberC.characterId }, 11), leader.ws);
  const [kickedEnv, updAfterKick] = await Promise.all([kickedEnvP, updAfterKickP]);
  console.log('memberC notificado de expulsión:', kickedEnv.payload);
  if (updAfterKick.payload.members.length !== 2) throw new Error('FALLO: el gremio debería quedar con 2 miembros tras la expulsión');
  console.log('OK: expulsión correcta.');

  console.log('\n=== 10. leader intenta expulsarse a sí mismo -> debe fallar ===');
  send(envelope('guild_kick', { target_character_id: leader.characterId }, 12), leader.ws);
  const selfKickErr = await waitFor(leader, (m) => m.type === 'error');
  console.log('error esperado:', selfKickErr.payload);
  if (selfKickErr.payload.code !== 'invalid_state') throw new Error('FALLO: se esperaba invalid_state al auto-expulsarse');

  console.log('\n=== 11. leader se va -> liderazgo pasa a memberB (único restante) ===');
  const afterLeaveB = waitFor(memberB, (m) => m.type === 'guild_update' && m.payload.members.length === 1);
  send(envelope('guild_leave', {}, 13), leader.ws);
  const b2 = await afterLeaveB;
  const newLeader = b2.payload.members[0];
  if (newLeader.character_id !== memberB.characterId || !newLeader.is_officer) throw new Error('FALLO: el liderazgo no pasó a memberB');
  console.log('OK: liderazgo transferido correctamente, memberB quedó solo como líder.');

  console.log('\n=== 12. memberB se va (último miembro) -> gremio disuelto ===');
  const disbandP = waitFor(memberB, (m) => m.type === 'guild_disbanded' && m.payload.reason === 'empty');
  send(envelope('guild_leave', {}, 14), memberB.ws);
  const disband = await disbandP;
  console.log('guild_disbanded:', disband.payload);
  if (disband.payload.guild_id !== guildId) throw new Error('FALLO: guild_disbanded con guild_id incorrecto');
  console.log('OK: el gremio se disolvió al quedar vacío.');

  console.log('\n=== 13. crear un gremio nuevo con el mismo nombre ahora debe funcionar (el anterior ya no existe) ===');
  const recreated = waitFor(memberC, (m) => m.type === 'guild_update' && m.payload.members.length === 1);
  send(envelope('guild_create', { name: guildName }, 15), memberC.ws);
  const recreatedUpd = await recreated;
  const newGuildId = recreatedUpd.payload.guild_id;
  console.log('OK: nombre reutilizable tras disolución.');

  console.log('\n=== 14. memberC se desconecta y reconecta -> guild_info debe devolverle su gremio (persistente, no arranca vacío como party) ===');
  memberC.ws.close();
  await new Promise((r) => setTimeout(r, 300));
  const memberC2 = await connectAndLogin(names[2], 'pass1234');
  const infoP = waitFor(memberC2, (m) => m.type === 'guild_update' && m.payload.guild_id === newGuildId);
  send(envelope('guild_info', {}, 1), memberC2.ws);
  const info = await infoP;
  if (info.payload.members.length !== 1 || info.payload.members[0].character_id !== memberC2.characterId) {
    throw new Error('FALLO: guild_info no devolvió el gremio esperado tras reconectar');
  }
  console.log('OK: guild_info devolvió el gremio persistente correctamente tras reconectar.', info.payload);

  console.log('\nTodos los escenarios de gremios pasaron.');
  leader.ws.close(); memberB.ws.close(); memberC2.ws.close(); outsider.ws.close();
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
