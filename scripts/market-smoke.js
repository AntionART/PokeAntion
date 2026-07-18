import { WebSocket } from 'ws';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';

const HTTP = 'http://localhost:8080';
const WS_URL = 'ws://localhost:8080/ws';
const PSQL = 'C:/Users/AuxSistemas/Pictures/Antion/pokemon-online/postgresql-16.5/pgsql/bin/psql.exe';
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
  const sellerUser = `seller_${suffix}`, buyerUser = `buyer_${suffix}`, poorUser = `poor_${suffix}`;

  console.log('=== 1. Registrar vendedor, comprador, y un comprador sin plata ===');
  const sellerReg = await register(sellerUser, 'password123');
  const buyerReg = await register(buyerUser, 'password123');
  const poorReg = await register(poorUser, 'password123');

  const monId = randomUUID();
  const cols = `(id, owner_char_id, species_id, nickname, level, hp_current, hp_max, stat_attack, stat_defense, stat_sp_attack, stat_sp_defense, stat_speed, nature, location, team_slot)`;
  psql(`INSERT INTO pokemon ${cols} VALUES ('${monId}', '${sellerReg.character_id}', 6, 'Charli', 10, 30, 30, 12, 12, 12, 12, 12, 4, 'team', 0);`);
  psql(`UPDATE characters SET money = 100 WHERE id = '${poorReg.character_id}';`); // no le alcanza para nada caro

  const seller = await connectAndLogin(sellerUser, 'password123');
  const buyer = await connectAndLogin(buyerUser, 'password123');
  const poor = await connectAndLogin(poorUser, 'password123');
  console.log('seller:', seller.characterId, 'buyer:', buyer.characterId, 'poor:', poor.characterId);

  console.log('\n=== 2. El vendedor publica su Charmander en 1500 ===');
  send(envelope('market_list', { pokemon_id: monId, price: 1500 }), seller.ws);
  const listOk = await waitFor(seller.ws, 'seller', (m) => m.type === 'market_list_ok');
  const listingId = listOk.payload.listing_id;
  console.log('listing_id:', listingId);

  const lockedLocation = psql(`SELECT location FROM pokemon WHERE id = '${monId}';`);
  if (lockedLocation !== 'in_trade') throw new Error(`FALLO: el pokémon publicado debería estar bloqueado (in_trade), está en "${lockedLocation}"`);
  console.log('OK: el pokémon queda bloqueado mientras está publicado.');

  console.log('\n=== 3. Explorar mercado: el comprador debe verlo listado ===');
  send(envelope('market_browse', {}), buyer.ws);
  const browsed = await waitFor(buyer.ws, 'buyer', (m) => m.type === 'market_listings');
  const found = browsed.payload.listings.find((l) => l.listing_id === listingId);
  if (!found) throw new Error('FALLO: la publicación no aparece en market_browse');
  if (found.price !== 1500 || found.pokemon.species_id !== 6 || found.seller_nickname !== sellerUser) {
    throw new Error(`FALLO: datos de la publicación incorrectos: ${JSON.stringify(found)}`);
  }
  console.log('OK: market_browse muestra la publicación con precio/especie/vendedor correctos.');

  console.log('\n=== 4. Un comprador sin plata suficiente no puede comprar ===');
  send(envelope('market_buy', { listing_id: listingId }), poor.ws);
  const poorFail = await waitFor(poor.ws, 'poor', (m) => m.type === 'error');
  console.log('error esperado:', poorFail.payload.message);

  console.log('\n=== 5. El vendedor no puede comprar su propia publicación ===');
  send(envelope('market_buy', { listing_id: listingId }), seller.ws);
  const selfBuyFail = await waitFor(seller.ws, 'seller', (m) => m.type === 'error');
  console.log('error esperado:', selfBuyFail.payload.message);

  console.log('\n=== 6. El comprador (con plata) compra: debe recibir el pokémon y el vendedor debe cobrar ===');
  const buyerMoneyBefore = Number(psql(`SELECT money FROM characters WHERE id = '${buyer.characterId}';`));
  const sellerMoneyBefore = Number(psql(`SELECT money FROM characters WHERE id = '${seller.characterId}';`));

  const purchasedP = waitFor(buyer.ws, 'buyer', (m) => m.type === 'market_purchased');
  const soldP = waitFor(seller.ws, 'seller', (m) => m.type === 'market_sold');
  send(envelope('market_buy', { listing_id: listingId }), buyer.ws);
  const [purchased, sold] = await Promise.all([purchasedP, soldP]);
  console.log('market_purchased (comprador):', JSON.stringify(purchased.payload));
  console.log('market_sold (vendedor):', JSON.stringify(sold.payload));

  if (purchased.payload.pokemon.id !== monId || purchased.payload.price !== 1500) {
    throw new Error('FALLO: market_purchased no trae el pokémon/precio correcto');
  }
  if (sold.payload.buyer_nickname !== buyerUser || sold.payload.price !== 1500) {
    throw new Error('FALLO: market_sold no trae el comprador/precio correcto');
  }

  const ownerNow = psql(`SELECT owner_char_id, location FROM pokemon WHERE id = '${monId}';`);
  const [ownerNowId, locationNow] = ownerNow.split('|');
  if (ownerNowId !== buyer.characterId || locationNow !== 'pc') {
    throw new Error(`FALLO: el pokémon no pasó al comprador correctamente: ${ownerNow}`);
  }
  const buyerMoneyAfter = Number(psql(`SELECT money FROM characters WHERE id = '${buyer.characterId}';`));
  const sellerMoneyAfter = Number(psql(`SELECT money FROM characters WHERE id = '${seller.characterId}';`));
  if (buyerMoneyAfter !== buyerMoneyBefore - 1500) throw new Error(`FALLO: al comprador no se le descontó 1500 (antes=${buyerMoneyBefore} después=${buyerMoneyAfter})`);
  if (sellerMoneyAfter !== sellerMoneyBefore + 1500) throw new Error(`FALLO: al vendedor no se le acreditó 1500 (antes=${sellerMoneyBefore} después=${sellerMoneyAfter})`);
  console.log(`OK: dinero transferido correctamente (comprador -1500, vendedor +1500), pokémon transferido a location='pc'.`);

  console.log('\n=== 7. Publicar y cancelar: el pokémon vuelve a estar libre ===');
  const monId2 = randomUUID();
  psql(`INSERT INTO pokemon ${cols} VALUES ('${monId2}', '${seller.characterId}', 1, 'Bulbi', 5, 20, 20, 10, 10, 10, 10, 10, 1, 'pc', NULL);`);
  send(envelope('market_list', { pokemon_id: monId2, price: 500 }), seller.ws);
  const listOk2 = await waitFor(seller.ws, 'seller', (m) => m.type === 'market_list_ok');
  send(envelope('market_cancel', { listing_id: listOk2.payload.listing_id }), seller.ws);
  await waitFor(seller.ws, 'seller', (m) => m.type === 'market_cancelled');
  const afterCancel = psql(`SELECT location FROM pokemon WHERE id = '${monId2}';`);
  if (afterCancel !== 'pc') throw new Error(`FALLO: tras cancelar debería volver a 'pc', está en "${afterCancel}"`);
  console.log('OK: al cancelar, el pokémon vuelve a estar disponible (location=pc).');

  seller.ws.close(); buyer.ws.close(); poor.ws.close();
  console.log('\nTodos los escenarios del mercado pasaron.');
}

main().catch((e) => { console.error('SMOKE TEST FAILED:', e); process.exit(1); });
