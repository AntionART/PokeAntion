# Pokémon Online

Servidor multijugador en Go (auth, mundo compartido, chat, trading, amigos, grupos) +
cliente propio en C#/.NET que embebe un core real de emulación de GBA (mGBA vía libretro)
y lee la posición del jugador directo de la RAM de la ROM — sin Godot, sin motor de terceros.
Ver `Documentation/ARCHITECTURE.md` para el diseño original y `client-engine/README.md` para
todo el detalle del cliente (Fases D y E del roadmap).

**Estado real (no aspiracional)**: servidor completo y verificado con smoke tests reales
(sección 6). Cliente con el emulador embebido, ventana propia en Direct3D 11, posición del
jugador leída en vivo de la ROM, y sincronización real con el servidor — un jugador ve a otro
moverse en pantalla, con la posición viniendo de la RAM del emulador, no simulada. Ver
`client-engine/README.md` para el detalle fase por fase (D.1 a D.4, E, D.2b) y qué falta
(sprites reales, más ROMs validadas, UI de chat/trading en el cliente).

---

## 1. Requisitos

- Go 1.22+ (o el SDK extraído localmente, ver sección 3 si no hay Docker)
- Docker y Docker Compose (para Postgres + Redis locales) — **o** Postgres/Redis locales sin
  Docker, ver "Alternativa sin Docker" más abajo (es lo que se usó para validar todo esto)
- Para el cliente (`client-engine/`): .NET 10 SDK + Windows (Direct3D 11 vía Vortice.Windows,
  P/Invoke a Win32) — ver `client-engine/README.md`. Ya no depende de Godot.

## 2. Levantar la infraestructura local

```bash
docker compose up -d
```

Esto levanta Postgres en `localhost:5432` (usuario/clave `pokemon`/`pokemon`, base
`pokemon_online`) y Redis en `localhost:6379`. El esquema de `database/migrations/0001_init_schema.sql`
se aplica automáticamente al crear el contenedor (vía `docker-entrypoint-initdb.d`).

Si ya tenías el contenedor creado antes de agregar una migración nueva, hay que aplicarla a mano:

```bash
docker exec -i <container_postgres> psql -U pokemon -d pokemon_online < database/migrations/000X_nueva.sql
```

## 3. Levantar el servidor

```bash
cd server
go mod tidy   # descarga gorilla/websocket, lib/pq, google/uuid, golang.org/x/crypto
go run ./cmd/server
```

Variables de entorno opcionales (ver `internal/config/config.go`):

```bash
HTTP_PORT=8080
DATABASE_URL=postgres://pokemon:pokemon@localhost:5432/pokemon_online?sslmode=disable
REDIS_ADDR=localhost:6379
JWT_SECRET=cambia-esto-en-produccion
```

El servidor expone:
- `GET /health` — chequeo simple.
- `POST /register` — crear cuenta + primer personaje.
- `GET /ws` (upgrade a WebSocket) — todo lo demás (login, movimiento, chat, trading, amigos, grupos).

### Alternativa sin Docker (Windows, sin daemon de Docker disponible)

Si Docker no está disponible en el entorno, hay scripts en `scripts/` que levantan Postgres
desde un binario extraído localmente y compilan/corren el servidor con un SDK de Go extraído
a mano (sin depender de una instalación global de Go):

```powershell
powershell -File scripts\start-postgres.ps1   # initdb + pg_ctl start, si hace falta
powershell -File scripts\start-server.ps1      # compila server/ y corre server.exe
```

Redis puede correr como servicio de Windows local (`redis-server.exe`) apuntado por
`REDIS_ADDR`; si no está disponible, el rate limiting de chat **falla abierto** (deja pasar
los mensajes sin límite) en vez de tumbar el chat — se loguea un warning al conectar.

## 4. Probar sin cliente gráfico (smoke test manual)

Registrar una cuenta:

```bash
curl -X POST localhost:8080/register -H "Content-Type: application/json" -d '{
  "username": "ash", "email": "ash@example.com", "password": "pikachu123",
  "rom_id": "emerald_us", "nickname": "Ash"
}'
```

Conectarse por WebSocket (con `websocat` o similar) y enviar como primer mensaje:

```json
{"type":"login","payload":{"username":"ash","password":"pikachu123"}}
```

Deberías recibir `login_ok` con tu `character_id`, mapa y posición inicial. A partir de ahí
podés enviar `move`, `send_chat`, etc., siguiendo `common/protocol/PROTOCOL.md`.

## 5. Estructura del repo

```
database/migrations/   → esquema SQL versionado (fuente de verdad de los datos)
server/                → servidor Go (auth, mundo, chat, trade, social, websocket)
common/protocol/       → contrato de mensajes cliente<->servidor (documento fuente)
client-stub/Protocol/  → structs del protocolo en C#, consumidas directo por client-engine
client-engine/         → cliente real: emulador embebido + ventana D3D11 + WebSocket (ver su README)
memory-maps/           → un archivo JSON por ROM soportada, con sus direcciones de memoria
Documentation/          → arquitectura completa, roadmap, riesgos
scripts/                → smoke tests (Node) y scripts de arranque local (PowerShell)
```

## 6. Estado verificado

Este esqueleto no es solo código sin probar: se compiló con `go build` y se corrieron flujos
end-to-end reales contra Postgres y Redis (ver `scripts/*-smoke.js`), confirmando:

- Registro de cuenta + personaje (bcrypt, transacción SQL). ✅
- Login por WebSocket y recepción de `login_ok` con JWT real firmado (HS256). ✅
- Reconexión solo con `session_token` (sin usuario/contraseña), y rechazo de tokens
  falsificados. ✅
- Dos jugadores conectados simultáneamente: uno se mueve, el otro recibe `player_update`
  en tiempo real. ✅
- Chat local: un jugador manda `send_chat`, el otro recibe `chat_message`. ✅
- Rate limiting de chat vía Redis (INCR + EXPIRE por jugador): ráfaga de 8 mensajes con
  límite de 5/s → exactamente 5 pasan y 3 vuelven `error: rate_limited`; tras expirar la
  ventana, vuelve a funcionar normal. ✅
- **Intercambio completo**: solicitud → aceptación → oferta de ambos → doble confirmación →
  transacción atómica que cambia el dueño de ambos Pokémon en la misma operación de base de
  datos, sin duplicación, con log de auditoría (`trade_log`) y notificación correcta a
  **ambos** jugadores. ✅ Verificado directamente contra las filas de la tabla `pokemon`.
- Cancelación de trade por las tres vías: `trade_decline` explícito, desconexión abrupta a
  mitad de trade (con oferta ya puesta), y timeout automático a los 2 minutos de inactividad
  — las tres liberan el Pokémon bloqueado y notifican a ambos jugadores. ✅
- Amigos: solicitud por username, aceptación bidireccional en una sola transacción,
  `friend_list` con estado online real (vía Hub), `friend_status_update` push al
  conectar/desconectar, y `friend_remove`. ✅
- Grupos: invitar (solo el líder), aceptar/declinar, `party_update` a todos los miembros,
  transferencia automática de liderazgo al miembro más antiguo cuando el líder se va, y
  disolución automática (`party_disbanded`) cuando el grupo queda vacío. ✅
- Límite de velocidad de movimiento (`handleMove`): un movimiento normal se acepta y
  propaga; un teletransporte obvio (200 tiles instantáneos) se rechaza con `move_rejected`
  y la posición corregida, **sin propagarse nunca** a otros jugadores; tras el rechazo, el
  jugador puede seguir moviéndose normalmente. ✅ (`scripts/move-speed-smoke.js`)
- Tests de integración en Go para `trade.go` contra Postgres real (no mocks): 20 corridas de
  doble confirmación simultánea (dos goroutines, sin duplicación nunca), cancelar con oferta
  puesta (libera el Pokémon), rechazar ofrecer un Pokémon ajeno, y rechazar confirmar sin que
  el otro jugador haya ofrecido nada. ✅ (`go test ./internal/trade/...`, ver sección 6b)

Bugs reales encontrados y corregidos durante esta verificación (no solo features nuevas):
`AuthResult` sin tags JSON (`/register` devolvía PascalCase en vez de snake_case), y — el más
importante — **el servidor nunca cerraba el socket TCP subyacente tras un cierre limpio de
WebSocket**, dejando conexiones a medio cerrar indefinidamente (`internal/ws/connection.go`,
corregido con un `defer conn.Close()`).

Nota sobre `go.mod`: ya no tiene directivas `replace` (se quitaron; `go mod tidy` resuelve
todo contra el proxy oficial de Go sin problema en este entorno). `go 1.24` es el mínimo
requerido por `golang-jwt/jwt/v5` y `redis/go-redis/v9`. Logging vía `log/slog` (nivel
configurable con `LOG_LEVEL=debug|info|warn|error`), no `log.Printf`.

## 6b. Tests de integración de Go

Requieren una base de Postgres real (no hay mocks para el módulo de trading — es el más
sensible del proyecto). Crear una vez:

```powershell
$env:PGPASSWORD = "pokemon"
& postgresql-16.5\pgsql\bin\psql.exe -U pokemon -h localhost -p 5432 -d postgres -c "CREATE DATABASE pokemon_online_test OWNER pokemon;"
& postgresql-16.5\pgsql\bin\psql.exe -U pokemon -h localhost -p 5432 -d pokemon_online_test -f database\migrations\0001_init_schema.sql
```

Correr:
```powershell
cd server
$env:TEST_DATABASE_URL = "postgres://pokemon:pokemon@localhost:5432/pokemon_online_test?sslmode=disable"
go test ./internal/trade/... -v
```
Si no hay conexión a `TEST_DATABASE_URL` (ni al default), los tests se saltan (`t.Skip`) en
vez de fallar — no rompen `go build`/`go vet` en un entorno sin Postgres.

## 7. Siguientes pasos recomendados (en orden)

1. **Cliente**: overlay de UI real (texto, no solo cuadrados de color), validar direcciones
   de mapa/equipo/dinero (`map_bank`/`map_number`/`save_block`, ver
   `client-engine/README.md` — el primer intento no encontró el patrón, hace falta
   desreferenciar `gSaveBlock1Ptr` en vez de asumir una dirección fija), y arrancar el
   cliente directo en el mundo (sin el flujo de un jugador de la ROM).
2. UI de chat/amigos/grupos/trading en el cliente, conectada a los eventos ya expuestos por
   el protocolo (ver `common/protocol/PROTOCOL.md`) y por `ClientApp/Network/WebSocketClient.cs`.
3. Mejoras de protocolo detectadas al construir D.2b: snapshot inicial de jugadores en el
   mapa al conectarse (hoy no existe, solo hay `player_update` a partir del primer
   movimiento de cada uno), y evento explícito de desconexión de un tercero.
4. Escalabilidad (pruebas de carga), launcher y updater — dejar para el final.
5. Multi-ROM: agregar `firered_us.json`, `ruby_us.json`, etc. una vez validado el patrón con
   Esmeralda, y conseguir una ROM `BPEE` (US) real para validar `emerald_us.json` (hoy
   placeholder) con la misma metodología que ya funcionó para `emerald_es.json`.

## 8. Notas importantes

- El proyecto nunca aloja ni distribuye ROMs ni assets de Nintendo/Game Freak. El usuario
  aporta su propia ROM legalmente obtenida; solo se lee su memoria en tiempo de ejecución local.
- La contraseña se hashea con bcrypt antes de tocar la base de datos (`internal/auth/auth.go`).
  Nunca loguear ni almacenar contraseñas en texto plano.
- El módulo de trading (`internal/trade/trade.go`) es el más sensible del proyecto: cualquier
  cambio ahí debe ir acompañado de pruebas que verifiquen que no es posible duplicar un Pokémon
  (doble confirmación simultánea, desconexión a mitad de transacción, etc.).
