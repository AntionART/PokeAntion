// Smoke test real de la tienda: registra una cuenta con plata inicial (3000, ver /register),
// conecta un WebSocket real, pide el catálogo, compra un objeto, confirma el descuento de
// dinero y el objeto acreditado, y confirma que comprar de más (sin plata) falla limpio.
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
    body: JSON.stringify({ username, email: `${username}@example.com`, password, rom_id: 'emerald_es', nickname: 'ShopTest', starter_species: 280 })
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

const username = `shoptest_${randomUUID().slice(0, 8)}`;
console.log('=== 1. Registrar cuenta (money inicial real, ver /register) ===');
const reg = await register(username, 'testpass123');
console.log('character_id:', reg.character_id, 'money inicial:', reg.money);

const ws = new WebSocket(WS_URL);
await new Promise((resolve, reject) => { ws.on('open', resolve); ws.on('error', reject); });
send(envelope('login', { username, password: 'testpass123' }), ws);
const loginOk = await waitFor(ws, m => m.type === 'login_ok' || m.type === 'login_error');
if (loginOk.type !== 'login_ok') throw new Error('login falló: ' + JSON.stringify(loginOk.payload));

console.log('\n=== 2. Pedir catálogo de la tienda ===');
send(envelope('shop_catalog_request', {}), ws);
const catalog = await waitFor(ws, m => m.type === 'shop_catalog');
const potion = catalog.payload.items.find(i => i.name === 'POTION');
if (!potion) throw new Error('POTION no está en el catálogo: ' + JSON.stringify(catalog.payload));
console.log(`POTION cuesta ${potion.price} (esperado 300, precio real de pokeemerald)`);
if (potion.price !== 300) throw new Error(`precio inesperado: ${potion.price}`);

console.log('\n=== 3. Comprar 2 Potions ===');
send(envelope('buy_item', { item_id: potion.item_id, quantity: 2 }), ws);
const bought = await waitFor(ws, m => m.type === 'buy_result' || m.type === 'error');
if (bought.type !== 'buy_result') throw new Error('compra falló: ' + JSON.stringify(bought.payload));
const expectedMoney = reg.money - potion.price * 2;
if (bought.payload.new_money !== expectedMoney) throw new Error(`new_money = ${bought.payload.new_money}, esperaba ${expectedMoney}`);
const expectedQty = 3 + 2; // 3 del kit inicial de registro + 2 recien comprados
if (bought.payload.new_quantity !== expectedQty) throw new Error(`new_quantity = ${bought.payload.new_quantity}, esperaba ${expectedQty}`);
console.log(`OK: gastó ${bought.payload.total_cost}, le quedan ${bought.payload.new_money}, tiene ${bought.payload.new_quantity} Potions`);

console.log('\n=== 4. Verificar contra la base de datos ===');
const dbMoney = parseInt(psql(`SELECT money FROM characters WHERE id = '${reg.character_id}';`), 10);
if (dbMoney !== expectedMoney) throw new Error(`money en DB = ${dbMoney}, esperaba ${expectedMoney}`);
const dbQty = parseInt(psql(`SELECT quantity FROM inventory_items WHERE owner_char_id = '${reg.character_id}' AND item_id = ${potion.item_id};`), 10);
if (dbQty !== 5) throw new Error(`quantity en DB = ${dbQty}, esperaba 5`);
console.log(`OK: DB confirma money=${dbMoney}, potions=${dbQty}`);

console.log('\n=== 5. Comprar más de lo que alcanza la plata (debe fallar limpio) ===');
send(envelope('buy_item', { item_id: potion.item_id, quantity: 999 }), ws);
const failedBuy = await waitFor(ws, m => m.type === 'buy_result' || m.type === 'error');
if (failedBuy.type !== 'error') throw new Error('esperaba un error de fondos insuficientes, llegó: ' + JSON.stringify(failedBuy));
console.log('OK: rechazado limpio ->', failedBuy.payload.message);

const dbMoneyAfter = parseInt(psql(`SELECT money FROM characters WHERE id = '${reg.character_id}';`), 10);
if (dbMoneyAfter !== expectedMoney) throw new Error(`money cambió tras la compra fallida: ${dbMoneyAfter}, esperaba ${expectedMoney} sin cambios`);
console.log('OK: la plata no se movió tras el intento fallido.');

console.log('\n=== 6. Comprar una cantidad válida (<=99) pero que no alcanza la plata ===');
const fullRestore = catalog.payload.items.find(i => i.name === 'FULL RESTORE');
send(envelope('buy_item', { item_id: fullRestore.item_id, quantity: 50 }), ws); // 50*3000 = 150000, muy por encima de lo que queda
const insufficientBuy = await waitFor(ws, m => m.type === 'buy_result' || m.type === 'error');
if (insufficientBuy.type !== 'error') throw new Error('esperaba error de fondos insuficientes: ' + JSON.stringify(insufficientBuy));
console.log('OK: rechazado por fondos insuficientes ->', insufficientBuy.payload.message);

ws.close();
console.log('\n=== TODO OK: tienda verificada de punta a punta contra el servidor real. ===');
