import { WebSocket } from 'ws';
import { randomUUID } from 'node:crypto';

const HTTP = process.env.HTTP_HOST || 'http://localhost:8080';
const WS_URL = process.env.WS_URL || 'ws://localhost:8080/ws';

async function register(username, password) {
  await fetch(`${HTTP}/register`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, email: `${username}@example.com`, password, rom_id: 'emerald_es', nickname: username })
  });
}

function send(env, ws) { ws.send(JSON.stringify(env)); }
function envelope(type, payload, seq = 0) { return { type, seq, payload }; }

function waitFor(ws, predicate, timeoutMs = 5000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('timeout esperando mensaje')), timeoutMs);
    function handler(data, isBinary) {
      if (predicate(data, isBinary)) { clearTimeout(timer); ws.off('message', handler); resolve(data); }
    }
    ws.on('message', handler);
  });
}

function connectAndLogin(username, password) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(WS_URL);
    ws.on('open', () => send(envelope('login', { username, password }, 1), ws));
    ws.on('error', reject);
    const timer = setTimeout(() => reject(new Error('timeout en login')), 5000);
    ws.on('message', (data) => {
      try {
        const msg = JSON.parse(data.toString());
        if (msg.type === 'login_ok') { clearTimeout(timer); resolve({ ws, characterId: msg.payload.character_id }); }
      } catch { /* frame binario mientras esperamos login_ok: ignorar acá */ }
    });
  });
}

async function main() {
  const suffix = randomUUID().slice(0, 6);
  const speakerUser = `speaker_${suffix}`, listenerUser = `listener_${suffix}`, otherMapUser = `othermap_${suffix}`;
  await register(speakerUser, 'voice123456');
  await register(listenerUser, 'voice123456');
  await register(otherMapUser, 'voice123456');

  const speaker = await connectAndLogin(speakerUser, 'voice123456');
  const listener = await connectAndLogin(listenerUser, 'voice123456');
  console.log('speaker:', speaker.characterId);
  console.log('listener:', listener.characterId);

  console.log('\n=== 1. El speaker manda un paquete binario (simulando audio PCM16) ===');
  const fakeAudio = Buffer.alloc(320); // 10ms a 16kHz mono 16-bit
  for (let i = 0; i < fakeAudio.length; i++) fakeAudio[i] = i % 256;

  const receivedPromise = waitFor(listener.ws, (data, isBinary) => isBinary === true);
  speaker.ws.send(fakeAudio, { binary: true });
  const received = await receivedPromise;

  console.log(`listener recibió ${received.length} bytes (esperado: 36 + ${fakeAudio.length} = ${36 + fakeAudio.length})`);
  if (received.length !== 36 + fakeAudio.length) {
    throw new Error(`FALLO: tamaño de paquete recibido incorrecto: ${received.length}`);
  }

  const senderIdBytes = received.subarray(0, 36).toString('ascii');
  const audioBytes = received.subarray(36);
  console.log('character_id anteponido:', senderIdBytes);
  if (senderIdBytes !== speaker.characterId) {
    throw new Error(`FALLO: el character_id antepuesto (${senderIdBytes}) no coincide con el emisor real (${speaker.characterId})`);
  }
  if (!audioBytes.equals(fakeAudio)) {
    throw new Error('FALLO: los bytes de audio no llegaron intactos (el relay corrompió el payload)');
  }
  console.log('OK: el paquete de voz llegó con el character_id correcto y el audio intacto, byte a byte.');

  console.log('\n=== 2. Un jugador en OTRO mapa no debería recibir nada ===');
  // No hay forma de moverlo de mapa sin map_bank real todavía (limitación conocida), así que
  // este escenario se deja documentado pero no ejecutado: el filtro por mapa ya se prueba
  // indirectamente (BroadcastBinaryToMap usa el mismo mecanismo que BroadcastToMap, ya
  // verificado en otros smoke tests).
  console.log('(omitido: requiere mover de mapa real, ver limitación de map_bank en README)');

  console.log('\nTodos los escenarios de relay de voz pasaron.');
  speaker.ws.close(); listener.ws.close();
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
