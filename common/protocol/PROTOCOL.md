# Protocolo Cliente ↔ Servidor

Transporte: **WebSocket** (JSON en texto para el MVP; se puede migrar a MessagePack/Protobuf después sin
romper la arquitectura, porque toda esta capa está aislada del resto del sistema).

Todo mensaje tiene esta envoltura común:

```json
{
  "type": "string",     // identificador del mensaje, ver tabla abajo
  "seq": 123,            // número de secuencia incremental (para detectar pérdidas/orden)
  "payload": { ... }     // datos específicos del tipo de mensaje
}
```

Convención de nombres: `snake_case` para `type`, request del cliente termina en verbo de acción
(`move`, `send_chat`), respuesta/evento del servidor termina en sustantivo o participio
(`player_update`, `trade_completed`).

---

## 1. Autenticación

### Cliente → Servidor: `login`
```json
{ "type": "login", "payload": { "username": "ash", "password": "..." } }
```

### Servidor → Cliente: `login_ok`
```json
{ "type": "login_ok", "payload": {
    "account_id": "uuid", "character_id": "uuid", "session_token": "jwt-o-similar",
    "map_id": "littleroot_town", "pos_x": 10, "pos_y": 12, "color": "default"
}}
```
`color` es el color de sprite persistido del personaje (ver sección 2 más abajo — "default" si
nunca lo cambió).

### Servidor → Cliente: `login_error`
```json
{ "type": "login_error", "payload": { "code": "invalid_credentials", "message": "..." } }
```

Después del login, todo mensaje subsiguiente en el socket usa la sesión ya autenticada
(no hace falta reenviar el token en cada mensaje si el socket persiste).

---

## 2. Movimiento y presencia (mundo compartido)

### Cliente → Servidor: `move`
```json
{ "type": "move", "payload": {
    "map_id": "littleroot_town", "x": 11, "y": 12, "facing": "up", "state": "walking"
}}
```
El servidor valida velocidad razonable (no más de `maxTilesPerSecond`, ver
`internal/ws/hub.go`) antes de aceptar. La validación de colisión contra el tilemap real
queda pendiente (el servidor todavía no tiene datos de mapas — ver roadmap). Si la velocidad
es inválida (teletransporte obvio), responde `move_rejected` **solo al emisor**, sin
propagar el movimiento a nadie más:
```json
{ "type": "move_rejected", "payload": {
    "map_id": "littleroot_town", "x": 11, "y": 12, "facing": "up"
}}
```
`x`/`y` son la última posición válida conocida por el servidor — el cliente debe resincronizarse a esa posición.

### Servidor → Clientes del mismo mapa: `player_update`
```json
{ "type": "player_update", "payload": {
    "character_id": "uuid", "nickname": "Ash", "sprite_id": "boy_1",
    "map_id": "littleroot_town", "x": 11, "y": 12, "facing": "up", "state": "walking",
    "color": "default"
}}
```
Los clientes remotos **interpolan** desde la última posición conocida hacia esta.
`nickname`/`color` siempre van resueltos por el servidor (Hub), nunca copiados del payload del
cliente — un jugador que se movió una vez y nunca más recibe otro tipo de mensaje sigue
apareciéndole a los demás con su nombre y color correctos en cada `move` posterior.

### Cliente → Servidor: `set_color`
```json
{ "type": "set_color", "payload": { "color": "red" } }
```
Cambia el color de sprite del emisor — tinte multiplicativo aplicado en el cliente sobre el
sprite ya decodificado (ver client-engine/ClientApp/SpriteColors.cs), no un sprite distinto.
Paleta fija y validada server-side (`internal/world/colors.go` — `AllowedSpriteColors`):
`default`, `red`, `blue`, `green`, `yellow`, `purple`, `orange`, `pink`, `cyan`. Un valor fuera
de esa lista responde `error` (`invalid_state`). Si se acepta: se persiste en
`characters.sprite_color`, y se manda un `player_update` con el color nuevo tanto al propio
emisor (confirmación) como al resto del mapa (broadcast, mismo mensaje que un `move` normal).

### Servidor → Cliente: `player_joined_map` / `player_left_map`
Se envía cuando otro jugador entra o sale del mapa actual (para instanciar/destruir su sprite
localmente sin tener que recibir el estado completo del mundo en cada tick). `player_left_map`
también se emite cuando ese jugador se **desconecta** (no solo al cambiar de mapa) — el mismo
mensaje cubre ambos casos, con `map_id` = el mapa que el cliente remoto debe limpiar.

### Servidor → Cliente: `map_players_snapshot`
Se manda **una sola vez**, inmediatamente después de `login_ok`, con la lista de jugadores
que ya están en el mapa de spawn (excluyéndote a vos mismo). Sin esto, un cliente recién
conectado no vería a nadie hasta que esa otra persona mandara su propio `move`.
```json
{ "type": "map_players_snapshot", "payload": { "players": [
    { "character_id": "uuid", "nickname": "Misty", "sprite_id": "girl_1",
      "map_id": "littleroot_town", "x": 15, "y": 20, "facing": "down", "state": "idle",
      "color": "blue" }
]}}
```
Si no hay nadie más en el mapa, este mensaje no se manda (no hay un `payload.players: []` vacío).

---

## 3. Chat

### Cliente → Servidor: `send_chat`
```json
{ "type": "send_chat", "payload": {
    "channel": "local",           // global | local | private | guild | command
    "target_character_id": null,  // solo si channel = private
    "message": "hola!"
}}
```

### Servidor → Cliente(s): `chat_message`
```json
{ "type": "chat_message", "payload": {
    "channel": "local", "from_character_id": "uuid", "from_nickname": "Ash",
    "message": "hola!", "timestamp": "2026-07-14T12:00:00Z"
}}
```

Comandos (`/trade`, `/msg`, `/help`, etc.) se parsean en el cliente o servidor a partir de
`channel: "command"`; el servidor responde con `chat_message` de tipo sistema o dispara el
flujo correspondiente (ej. `/trade Ash` inicia `trade_request`).

---

## 4. Amigos

La amistad se guarda por `account_id` (no por personaje: es a nivel de cuenta, no de
personaje/ROM), pero se direcciona por `character_id` cuando el destinatario está conectado.

| Mensaje | Dirección | Payload |
|---|---|---|
| `friend_request` | C→S | `{ "target_username": "misty" }` |
| `friend_request_received` | S→C | `{ "from_account_id": "uuid", "from_username": "ash" }` |
| `friend_accept` / `friend_decline` | C→S | `{ "target_account_id": "uuid" }` (cuenta que envió la solicitud) |
| `friend_remove` | C→S | `{ "target_account_id": "uuid" }` |
| `friend_list` | C→S | `{}` — pide la lista actual |
| `friend_list` | S→C | `{ "friends": [ { "account_id", "username", "online" }, ... ] }` |
| `friend_status_update` | S→C | `{ "account_id": "uuid", "online": true }` — push cuando un amigo se conecta/desconecta |

La aceptación crea la relación en ambas direcciones (A→B y B→A) en una sola transacción,
así que `friend_list` funciona igual sin importar quién mandó la solicitud original.

---

## 5. Grupos (party)

Las invitaciones pendientes viven solo en memoria del proceso servidor (no hay tabla propia
en el esquema para ellas, a diferencia de `trade_sessions`): son efímeras, igual que la
presencia del Hub. `party_groups`/`party_members` sí son persistentes.

| Mensaje | Dirección | Payload |
|---|---|---|
| `party_invite` | C→S | `{ "target_character_id": "uuid" }` — solo el líder puede invitar |
| `party_invite_received` | S→C | `{ "party_id", "from_character_id", "from_nickname" }` |
| `party_accept` / `party_decline` | C→S | `{ "party_id": "uuid" }` |
| `party_update` | S→C (a todos los miembros) | `{ "party_id", "members": [ { "character_id", "nickname", "map_id", "x", "y", "is_leader" }, ... ] }` |
| `party_leave` | C→S | `{}` |
| `party_disbanded` | S→C | `{ "party_id", "reason" }` — se emite al último miembro cuando el grupo queda vacío |

Si el líder sale y quedan más miembros, el liderazgo pasa automáticamente al miembro más
antiguo del grupo (no hay elección). Al desconectarse, un jugador sale de su grupo igual
que si mandara `party_leave` explícito.

---

## 6. Intercambios (trading)

Máquina de estados (ver también `trade_sessions` en la base de datos):

```
pending → accepted → offering → (confirmed_a + confirmed_b) → completed
                                                             ↘ cancelled / failed_rollback
```

| Mensaje | Dirección | Payload relevante |
|---|---|---|
| `trade_request` | C→S | `target_character_id` |
| `trade_request_received` | S→C | `trade_session_id`, `from_character_id` |
| `trade_accept` / `trade_decline` | C→S | `trade_session_id` |
| `trade_offer_set` | C→S | `trade_session_id`, `pokemon_id` |
| `trade_offer_update` | S→C (a ambos) | oferta actual de ambos jugadores |
| `trade_confirm` | C→S | `trade_session_id` |
| `trade_completed` | S→C (a ambos) | resultado final, IDs de Pokémon intercambiados |
| `trade_cancelled` | S→C (a ambos) | `reason` |

Regla dura: entre `trade_offer_set` y `trade_completed`/`trade_cancelled`, los Pokémon
ofrecidos quedan con `location='in_trade'` y `locked_for_trade_id` en base de datos —
no pueden usarse en batalla, PC, ni otro trade simultáneo.

---

## 7. Mercado (comercio asincrónico)

A diferencia del trade (sección 6), el mercado **no requiere que ambos jugadores estén
online al mismo tiempo**: publicar y comprar son operaciones independientes. Reutiliza
`pokemon.location='in_trade'` y `locked_for_trade_id` para bloquear el Pokémon publicado (sin
agregar un valor de `location` nuevo ni una constraint de esquema distinta a la de trade).

| Mensaje | Dirección | Payload |
|---|---|---|
| `market_list` | C→S | `{ "pokemon_id": "uuid", "price": 1500 }` — debe ser dueño y el Pokémon no puede estar ya bloqueado |
| `market_list_ok` | S→C | confirmación al vendedor |
| `market_cancel` | C→S | `{ "listing_id": "uuid" }` — solo el vendedor puede cancelar su propia publicación |
| `market_cancelled` | S→C | confirmación al vendedor; el Pokémon vuelve a `location='pc'` |
| `market_browse` | C→S | `{}` — pide las publicaciones activas de todos |
| `market_my_listings` | C→S | `{}` — pide solo las propias |
| `market_listings` / `market_my_listings` | S→C | `{ "listings": [ { "listing_id", "seller_char_id", "seller_nickname", "pokemon", "price" }, ... ] }` — **tipos de mensaje distintos** aunque comparten forma: `market_browse` responde con `market_listings`, `market_my_listings` responde con el mismo nombre de tipo que el pedido; el cliente no debe asumir que ambos pedidos comparten un único handler, o la segunda respuesta pisa a la primera |
| `market_buy` | C→S | `{ "listing_id": "uuid" }` — falla si es publicación propia o si no alcanza el dinero |
| `market_purchased` | S→C (al comprador) | `{ "listing_id", "pokemon", "price" }` |
| `market_sold` | S→C (al vendedor, si está online) | `{ "listing_id", "buyer_nickname", "price" }` — si el vendedor está offline, se entera al reabrir "mis publicaciones" |

La compra es una transacción atómica: débito al comprador, crédito al vendedor
(`characters.money`), transferencia de dueño del Pokémon y marcado de la publicación como
`sold`, todo o nada.

---

## 8. Gremios (guilds)

A diferencia de un grupo (sección 5), un gremio es **persistente**: sobrevive a que todos sus
miembros se desconecten, y solo se disuelve cuando el último miembro se va explícitamente
(`guild_leave`). Por eso el servidor no empuja el estado de un gremio espontáneamente al
loguear — hace falta pedirlo con `guild_info` (a diferencia de party/trade, que siempre
arrancan vacíos para una conexión nueva).

| Mensaje | Dirección | Payload |
|---|---|---|
| `guild_create` | C→S | `{ "name": "..." }` — falla si el nombre ya existe o si ya estás en un gremio |
| `guild_invite` | C→S | `{ "target_character_id": "uuid" }` — solo el líder puede invitar; el objetivo no puede estar ya en un gremio |
| `guild_invite_received` | S→C | `{ "guild_id", "guild_name", "from_character_id", "from_nickname" }` |
| `guild_accept` / `guild_decline` | C→S | `{ "guild_id": "uuid" }` |
| `guild_leave` | C→S | `{}` |
| `guild_kick` | C→S | `{ "target_character_id": "uuid" }` — solo el líder, no puede expulsarse a sí mismo (usar `guild_leave`) |
| `guild_info` | C→S | `{}` — pide el gremio actual (o nada, si no estás en uno); necesario tras reconectar |
| `guild_update` | S→C (a todos los miembros) | `{ "guild_id", "name", "members": [ { "character_id", "nickname", "online", "is_officer" }, ... ] }` — `nickname` se resuelve contra la base (no contra el Hub de conexiones), así los miembros offline también aparecen con su nombre correcto |
| `guild_disbanded` | S→C | `{ "guild_id", "reason" }` — `reason: "empty"` (te fuiste y eras el último) o `"kicked"` (te expulsaron) |

Si el líder sale y quedan más miembros, el liderazgo pasa automáticamente al miembro más
antiguo restante (mismo criterio que party). El canal de chat `"guild"` (sección 3) manda a
todos los miembros del gremio del emisor, incluyéndolo a él mismo (eco), igual que `"private"`.

---

## 9. Batallas PvE (solo reporte de resultado, no simulación en servidor)

### Cliente → Servidor: `battle_result`
```json
{ "type": "battle_result", "payload": {
    "outcome": "win",
    "team_snapshot": [ { "pokemon_id": "uuid", "hp_current": 12, "experience": 450 }, ... ],
    "items_gained": [ { "item_id": 4, "quantity": 1 } ],
    "money_delta": 320
}}
```
El servidor valida que los deltas sean razonables (anti-cheat básico: límites de experiencia/dinero
por batalla) antes de persistir. Preparado para, en el futuro, añadir `battle_request`/`battle_turn`
cuando se implemente PvP con el servidor arbitrando turnos.

---

## 10. Errores genéricos

```json
{ "type": "error", "payload": { "code": "rate_limited", "message": "..." } }
```

Códigos reservados: `invalid_credentials`, `session_expired`, `rate_limited`, `invalid_state`,
`not_found`, `forbidden`, `internal_error`.
