# Pokémon Online

Servidor multijugador en Go (auth, mundo compartido, chat, trading, amigos, grupos, mercado,
gremios, batalla PvP) + cliente propio en C#/.NET que embebe un core real de emulación de GBA
(mGBA vía libretro) y lee/escribe la RAM de la ROM en vivo (posición, dinero, equipo) — sin
Godot, sin motor de terceros. Ver `Documentation/ARCHITECTURE.md` para el diseño original y
`client-engine/README.md` para el detalle del cliente.

**Estado real (no aspiracional, última revisión 2026-07-29)**: servidor completo con UI real en
el cliente (texto/paneles propios, no cuadrados de color) para chat, amigos, grupos, trade,
mercado, gremios y batalla PvP — no solo protocolo sin interfaz. El sistema de batalla es
server-autoritativo de punta a punta (motor de daño real de Gen3, catálogo completo de ~386
especies/~355 movimientos generado del código fuente real de pokeemerald, menú Fight/Bag/
Pokémon/Run) y fue verificado con dos conexiones WebSocket independientes completando una
pelea real (`scripts/battle-smoke.js`), no solo con un cliente + datos sintéticos. Ver sección 6
para el detalle de qué está probado y sección 7 para lo que falta de verdad.

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
data/pokemon/           → species.json/moves.json generados por server/cmd/gendata (ver abajo)
server/                → servidor Go (auth, mundo, chat, trade, social, battle, websocket)
server/cmd/gendata/    → parsea el código fuente de pokeemerald y regenera data/pokemon/*.json
common/protocol/       → contrato de mensajes cliente<->servidor (documento fuente)
client-stub/Protocol/  → structs del protocolo en C#, consumidas directo por client-engine
client-engine/         → cliente real: emulador embebido + ventana D3D11 + WebSocket (ver su README)
memory-maps/           → un archivo JSON por ROM soportada, con sus direcciones de memoria
Documentation/          → arquitectura completa, roadmap, riesgos
scripts/                → smoke tests (Node) y scripts de arranque local (PowerShell)
```

### Regenerar el catálogo de especies/movimientos

`data/pokemon/species.json` y `moves.json` (~386 especies, ~355 movimientos, con nombre/tipos/
stats/poder/precisión/PP reales) se generan del checkout local de `pokeemerald-master`
(`Pokemon Esmeralda/pokeemerald-master/pokeemerald-master/`, no incluido en el repo — es el
código fuente de la descompilación de Emerald). Si ese checkout cambia, regenerar con:

```bash
cd server
go run ./cmd/gendata
```

Tanto el servidor (`pokemon.LoadSpeciesCatalog`/`battle.LoadMoveCatalog`, cargados al arrancar
desde `DATA_DIR`, default `../data/pokemon`) como el cliente (`ClientApp/Battle/PokedexCatalog`)
leen estos mismos dos archivos — una sola fuente de verdad.

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
- **Batalla PvP, servidor-autoritativa de punta a punta** (`internal/battle` +
  `internal/battlesession`): motor de daño real de Gen3 (tipo/crítico/STAB/orden por
  Velocidad), catálogo completo de ~386 especies/~355 movimientos generado del código fuente
  real de pokeemerald (`server/cmd/gendata`), cambio de Pokémon a mitad de combate (con cambio
  forzado si el activo se debilita y queda equipo vivo), Huir (rendición inmediata), y Bag con
  7 objetos de curación reales que de verdad consumen inventario (`internal/inventory`). Nunca
  otorga experiencia/nivel (deliberado, ver comentario en `battlesession.resolveExchange`) —
  solo el modo historia dentro del propio emulador sube de nivel. ✅ Verificado con **dos
  conexiones WebSocket independientes** completando una pelea real turno a turno hasta el final
  y un escenario de huida (`scripts/battle-smoke.js`), más tests de integración Go para el
  cambio forzado de Pokémon y el uso de objetos (`go test ./internal/battlesession/...`).
- Auth: creación de cuenta con contraseña realmente hasheada (no en texto plano), rechazo de
  usernames repetidos (`ErrUsernameTaken`, distinguido por código de constraint 23505 de
  Postgres — antes devolvía un error genérico), login con credenciales inválidas/usuario
  inexistente, y el ciclo completo de un JWT de sesión (válido, firmado con otro secreto,
  vencido). ✅ (`go test ./internal/auth/...`)
- **Carga concurrente real**: 50 cuentas reales, conectadas y logueadas al mismo tiempo, todas
  moviéndose y chateando en simultáneo durante 10 segundos en el mismo mapa — cero
  desconexiones inesperadas, cero errores de socket, el servidor siguió respondiendo `/health`
  normalmente después (`scripts/concurrent-load-smoke.js`, `LOAD_N`/`LOAD_DURATION_MS`
  configurables). Confirma que el diseño existente (Hub por-mapa, envío no bloqueante con
  descarte si el buffer se llena, pool de 25 conexiones a Postgres) efectivamente aguanta el
  escenario real de "varios amigos jugando juntos", no solo 1-2 jugadores como todos los smoke
  tests anteriores.
- **Pantallas de inicio con estilo real** (`client-engine/ClientApp/Screens/LoginFlow.cs`):
  login/registro/elección de inicial/elección de ROM pasaron de texto plano sobre negro a un
  panel enmarcado centrado (mismo lenguaje visual "cyberpunk-trainer" que ya usaba
  `BattleScreen`: bordes de acento, cajas de campo con resaltado de foco, filas de lista con
  highlight) — pedido explícito del usuario para que el arranque del juego se sienta como una
  interfaz real (referencia: PokeMMO), no una pantalla de depuración. ✅ Verificado con capturas
  reales del backbuffer y una prueba de foco con inyección de teclado real (ver
  `client-engine/README.md` sección D.5).
- **Curvas de experiencia reales por especie + aprendizaje de movimientos al subir de nivel**:
  las 6 fórmulas reales de Gen3 (Erratic/Fast/Medium Fast/Medium Slow/Slow/Fluctuating, portadas
  de `src/data/pokemon/experience_tables.h`) reemplazan la única curva universal que se usaba
  antes — `growth_rate` real de cada especie viaja en `data/pokemon/species.json` desde esta
  sesión. Subir de nivel ahora también aprende movimientos nuevos si el learnset real de la
  especie tiene uno en el rango de niveles saltado (hasta 4 movimientos; si ya tenía 4, el nuevo
  se descarta — no hay pantalla de "reemplazar movimiento" todavía, limitación conocida). ✅
  Verificado con tests reales contra Postgres, incluyendo los valores exactos de nivel 100/50 de
  las 6 curvas (1,000,000/600,000/1,640,000/1,059,860/800,000/1,250,000 — verificados
  independientemente contra las fórmulas fuente, no de memoria) y un caso real conocido (Torchic
  aprende el movimiento 116 en nivel 7). (`go test ./internal/pokemon/...`)
- **Cobertura de tests ampliada**: `social` (amigos/grupos/gremios — transferencia de liderazgo,
  disolución, invitaciones), `market` (compra/venta atómica, fondos insuficientes, cancelación),
  `chat` (unitario con fakes: ruteo por canal, rate limiting, fail-open si Redis cae), y `ws.Hub`
  (límite de velocidad de movimiento, agrupamiento por mapa, broadcast con exclusión) — ninguno
  tenía test antes de esta sesión. Sin bugs nuevos encontrados en esos 4 (código ya sólido);
  cierra la mayor parte del riesgo de cobertura documentado en la sección 6b. ✅ (`go test
  ./internal/social/... ./internal/market/... ./internal/chat/... ./internal/ws/...`)
- **Carga concurrente real**: 50 cuentas reales conectadas y jugando al mismo tiempo (mover +
  chatear) durante 10s sin ninguna caída ni error — confirma que el diseño ya existente aguanta
  el escenario real de "varios amigos jugando juntos" (`scripts/concurrent-load-smoke.js`).
- **Migraciones automatizadas**: `scripts/apply-migrations.ps1` con tabla de control
  (`schema_migrations`), aplicable contra ambas bases en una sola corrida, con detección de
  "bootstrap" para no romper una base que ya tenía el esquema aplicado a mano.
- **Empaquetado self-contained del cliente**: `scripts/publish-client.ps1` genera un build que
  no necesita .NET instalado en la PC destino. Encontró y corrigió una fragilidad real:
  `Program.cs` resolvía la raíz del repo con una cuenta fija de directorios (`..\..\..\..\..`)
  que se habría roto en silencio con cualquier profundidad de output distinta (Release, con
  `RuntimeIdentifier`, `dotnet publish`) — reemplazada por una búsqueda hacia arriba de
  `memory-maps/` + `data/pokemon/` juntos, robusta a cualquier configuración de build futura.
- **Encuentros salvajes + captura, 100% servidor-autoritativo** (`internal/wildencounter`,
  nuevo paquete, no reutiliza `battlesession`): el cliente nunca decide especie/nivel/IVs ni si
  una captura funciona — solo manda "estoy en el mapa X" y reacciona a lo que el servidor
  resuelve. Encuentro real de Gen3 (`encounterRate*16 contra Random()%2880`, tabla de pesos por
  slot de `src/data/wild_encounters.json`), IVs 0-31 aleatorias, stats calculados con la fórmula
  real, fórmula de captura real de Gen3 (Master Ball siempre atrapa, el resto según
  `catchRate`/HP restante/bonus de la ball con 4 tiradas de "shake"), inserción real de la fila
  en `pokemon` + upsert en `pokedex_entries` al atrapar, y experiencia real (`exp_yield`) al
  vencer sin atrapar — el único lugar del juego, junto con el modo historia, donde algo gana
  experiencia (PvP nunca da experiencia, decisión ya tomada). ✅ Verificado con tests de
  integración Go contra Postgres real (tasa de encuentro estadística, Master Ball 100%,
  atrapar-y-persistir) y un smoke test con una conexión WebSocket real completando un encuentro
  real en Route 101 de punta a punta (`scripts/wild-smoke.js`). Cliente: `BattleScreen` ahora
  soporta un modo salvaje (Luchar/Mochila con solo Poké Balls/Huir, sin menú Pokémon) verificado
  visualmente vía `--debug-wild-battle <especie propia> <especie salvaje>` (ver sección 7 para
  la limitación que queda abierta).
- **Pantalla de "reemplazar movimiento"**: cuando un Pokémon sube de nivel y aprende un
  movimiento nuevo pero ya tiene 4, el servidor manda `wild_move_replace_prompt` (aparte de
  `wild_battle_end`, no bloquea el fin de la pelea) y el cliente (`MoveReplacePrompt`, pantalla
  modal independiente de `BattleScreen`) deja elegir a cuál de los 4 reemplazar, o declinar —
  antes el movimiento nuevo simplemente se descartaba en silencio. `pokemon.ReplaceMove` valida
  ownership del Pokémon contra el `character_id` que manda la decisión (sin eso, cualquier
  jugador podría reescribir el moveset de un Pokémon ajeno adivinando su ID). ✅ Verificado con
  tests Go reales (reemplazo correcto, rechazo de no-dueño, rechazo de slot inválido) y
  visualmente vía `--debug-move-replace`.
- **Verificación de email (opcional, no bloqueante)**: `internal/email` — al registrarse se
  genera un token real (32 bytes) y se manda un correo con un link `/verify-email?token=...`.
  Compatible con Gmail (`smtp.gmail.com:587` + una "contraseña de aplicación", no la contraseña
  real de la cuenta — ver https://myaccount.google.com/apppasswords) vía `SMTP_HOST`/
  `SMTP_PORT`/`SMTP_USERNAME`/`SMTP_PASSWORD`/`SMTP_FROM`, pero no es específico de Gmail:
  cualquier SMTP con auth PLAIN + STARTTLS sirve. **Sin `SMTP_HOST` configurado (default de este
  entorno), el correo no se manda de verdad — se loguea en la consola del servidor**, para que
  registrarse siga funcionando de punta a punta sin credenciales reales (mismo criterio que el
  rate limiter de Redis: una pieza de infraestructura externa ausente nunca debe tumbar el
  producto). La cuenta es 100% usable sin verificar — esto no es un gate de acceso, es una
  mejora de confianza/recuperación. ✅ Verificado con tests Go reales (token generado, correo
  armado con el link correcto, verificación marca la cuenta y consume el token, reintento con
  el mismo token se rechaza) y un flujo real completo contra el servidor rebuildeado
  (`/register` → link logueado → `/verify-email?token=...` → "¡Cuenta verificada!" → reintento
  rechazado). Migración `0008_email_verification.sql`.
- **Encuentros de agua/pesca/rock-smash**: `internal/wildencounter` ya no solo conoce pasto
  alto (`land_mons`) — `water_mons`/`rock_smash_mons` usan exactamente la misma fórmula real de
  tasa que land (`encounterRate*16` contra `Random()%2880`, confirmado en
  `src/wild_encounter.c`: `WildEncounterCheck`/`RockSmashWildEncounter` llaman la misma
  función). Pesca es distinta: 3 sub-tablas por caña (`old_rod`/`good_rod`/`super_rod`) que NO
  se mezclan entre sí, sin chequeo de tasa propio (`ChooseWildMonIndex_Fishing` no tiene uno en
  el código real) — y **requiere tener la caña correspondiente** (nuevos ítems llave
  `ItemOldRod`/`ItemGoodRod`/`ItemSuperRod`, IDs reales de `include/constants/items.h`):
  `wildencounter.StartEncounter` valida posesión ANTES de tirar nada, así nadie puede pescar con
  una Super Rod que nunca tuvo con solo mandar el string correcto. Protocolo:
  `wild_encounter_triggered` ahora lleva `encounter_type` (`"land"` default/`"water"`/
  `"rock_smash"`/`"fishing"`) y `rod_tier`. `server/cmd/gendata` regenerado: 135 mapas con
  encuentros (antes 106 — los mapas que solo tenían agua/rocas y nada de pasto se estaban
  descartando enteros). ✅ Verificado con tests Go reales (tasa estadística de agua/rock smash,
  filtrado correcto por caña, rechazo sin caña, rechazo de caña inválida) y un smoke test
  end-to-end contra el servidor real cazando un Marill surfeando, un Magikarp pescando (con
  chequeo de caña real) y un Geodude con Rock Smash (`scripts/wild-water-fishing-rocksmash-smoke.js`).

- **Señal de RAM real para "empezó un encuentro salvaje nativo"**: `gMain.callback1`/
  `callback2` (`struct Main`, `include/main.h`) viven en IWRAM `0x03005D00`/`0x03005D04` para
  emerald_es — encontrados diffeando un volcado completo de IWRAM (`--dump-iwram`, nuevo flag
  de `ClientApp`) entre 8 muestras caminando y 2 muestras dentro de una pelea salvaje nativa
  real (Zigzagoon y Poochyena, cazados jugando de verdad en Route 101 tras cruzar un gate de
  historia real — ver `client-engine/README.md` para el detalle). `callback1` queda constante
  (`0x08085E19`) sea cual sea la pantalla; `callback2` cambia de `0x0803DF71` (overworld) a
  `0x0803B3CD` (en pelea) — documentado con la metodología completa en
  `memory-maps/emerald_es.json` (`wild_battle_signal`) y expuesto vía
  `IMemoryAdapter.IsCallback2AwayFromOverworld()`. **Actualización 2026-07-29: ya conectado y
  verificado de punta a punta.** `Program.cs` ahora detecta el flanco de subida cada frame y
  manda `wild_encounter_triggered` solo (gateado por `!battleActive`, sin `--debug-wild-battle`).
  Bug real encontrado en el camino: el cliente nunca resolvía un ID de mapa real fuera del mapa
  de login (mandaba `"spawn#N"`, un esquema sintético que el servidor jamás podía reconocer) —
  agregado `ClientApp/MapCatalog.cs` (lee `data/pokemon/maps.json`, igual que ya hace el
  servidor) para resolver `(mapGroup, mapNum)` al ID canónico real (`MAP_ROUTE101`) que
  `wildencounter.TryEncounter` necesita. ✅ Verificado en vivo: `--dump-iwram` confirmó
  `callback2 = 0x0803B3CD` en el instante exacto de tres peleas nativas reales (Zigzagoon,
  Poochyena, Wurmple en Route 101); confirmado además que abrir el menú START **no** genera
  falso positivo (`callback2` se mantiene en el valor de overworld). El pipeline
  servidor-cliente completo se confirmó independientemente con `scripts/wild-smoke.js` (cuenta
  con inicial real) — la cuenta de prueba `client_dev` de `--dev-boot` no tiene Pokémon en la
  base (registrada hace tiempo sin `starter_species`), así que no sirve para probar el loop
  completo con el cliente real sin darle uno a mano primero. Pendiente real: entrar a un
  edificio todavía no se probó específicamente (solo el menú START), y esta señal solo
  distingue "batalla" de "overworld" — siempre manda `encounter_type: "land"`, no diferencia
  agua/rocas (necesitaría una señal de RAM propia para eso).
- **Launcher y updater** (`client-engine/Launcher`, self-contained win-x64 propio): un .exe
  chico con una ventana de progreso que arranca preguntándole al servidor `GET
  /client-version` (nuevo endpoint, junto con `GET /client-download` que sirve el .zip armado
  por `scripts/build-client-bundle.ps1`), descarga/descomprime el bundle completo (ClientApp
  self-contained + `memory-maps/` + `data/pokemon/` + sprites de batalla — nunca la ROM, sigue
  la restricción legal de siempre) si la versión instalada localmente no coincide, y lanza
  `ClientApp.exe` — todo sin que un amigo tenga que instalar .NET ni copiar carpetas a mano. La
  versión sale de un único archivo `VERSION` en la raíz del repo, leído por el servidor
  (`config.ClientVersion`) y comparado contra un `client/VERSION` local que el propio Launcher
  escribe. Si el servidor está caído pero ya hay una instalación local, igual lanza esa versión
  (fail-open, mismo criterio que el resto del proyecto) en vez de bloquear. ✅ Verificado
  end-to-end de verdad: `build-client-bundle.ps1` corrido completo (produce un .zip de ~51MB),
  servidor reiniciado con el build nuevo, `Launcher.exe` corrido dos veces reales — primera vez
  hizo la instalación completa (descarga+extracción+`ClientApp.exe` real quedó corriendo,
  ventana respondiendo), segunda vez detectó que ya estaba actualizado y lanzó directo sin
  volver a descargar.
  **Actualización 2026-07-28/29: reescrito de cero en WPF** (antes WinForms) con arquitectura
  MVVM (`Launcher/{ViewModels,Services,Converters,Localization,Themes}/`) y ya no es solo una
  ventana de progreso que se autolanza — queda esperando en una pantalla principal (noticias/
  eventos, estado del servidor con ping real y jugadores conectados vía nuevos endpoints
  `GET /server-status` y `GET /news`, refrescados cada 15s) hasta que el jugador aprieta
  **JUGAR**. Detección/validación de ROM movida acá (antes vivía en `ClientApp.LoginFlow`): lee
  `memory-maps/*.json` de forma independiente (sin referenciar RomLoader/ClientApp — el launcher
  no depende del motor), valida por SHA1 real contra `rom_checksum_sha1`, y si no encuentra
  ninguna ROM válida muestra una pantalla de onboarding con selector de archivo (nunca descarga
  ni sugiere de dónde conseguir una) — `ClientApp` recibe `--rom/--memory-map/--rom-id` ya
  resueltos y `LoginFlow` salta la pantalla de selección cuando el catálogo trae una sola
  entrada. También: selector de idioma ES/EN, "recordar usuario" (solo precarga el campo de
  login, la contraseña nunca pasa por el launcher). ✅ `client-engine/Launcher.Tests` (xUnit,
  15 tests) cubre el parseo del catálogo de ROMs y la resolución/validación por hash — primer
  test automatizado del lado C# del proyecto (el servidor Go ya tenía cobertura fuerte, el
  cliente se verificaba solo a mano/en vivo).
- **Recuperación de contraseña por email** (mismo patrón que la verificación de email, migración
  `0009_password_reset.sql`): `POST /request-password-reset` `{email}` genera un token de 32
  bytes y manda (o loguea, sin SMTP configurado) un link `/reset-password?token=...`; **siempre**
  responde 200 con el mismo mensaje genérico exista o no una cuenta con ese email — no hay forma
  de usar este endpoint para enumerar cuentas registradas probando direcciones al azar. A
  diferencia del token de verificación de email (que no vence), el de recuperación vence en 1
  hora (`resetTokenTTL`) — un link de reset filtrado/reenviado por accidente es un riesgo real
  que uno de verificación no es. `POST /reset-password` `{token, new_password}` valida vigencia +
  consume el token de un solo uso. ✅ Verificado con 5 tests Go reales (token real en el correo,
  email desconocido no manda nada, reset cambia la contraseña de verdad, token reusado se
  rechaza, token vencido se rechaza forzando el timestamp en la base) y un flujo HTTP end-to-end
  contra el servidor real reconstruido: registrar cuenta → pedir reset → extraer el token
  logueado → resetear → confirmar por WebSocket que la contraseña vieja ya no entra y la nueva sí.

Bugs reales encontrados y corregidos durante esta verificación (no solo features nuevas):
`AuthResult` sin tags JSON (`/register` devolvía PascalCase en vez de snake_case); **el
servidor nunca cerraba el socket TCP subyacente tras un cierre limpio de WebSocket**, dejando
conexiones a medio cerrar indefinidamente (`internal/ws/connection.go`, corregido con un
`defer conn.Close()`); `accuracy == 0` en los datos de pokeemerald significa "nunca falla" (ej.
Swords Dance), no "0% de probabilidad" — invisible mientras el motor de batalla solo conocía 5
movimientos (ninguno con accuracy 0), rompía todo movimiento de estado real al cargar el
catálogo completo; `ErrUsernameTaken` estaba definido pero nunca se devolvía realmente
(`Register` envolvía cualquier error de Postgres sin distinguir el de username repetido).

**2026-07-29**: diagnosticado y corregido el test flaky de `TestItem_HealAndConsumesInventory`
(`internal/battlesession`) que aparecía "1 de cada pocas corridas" — no era mala suerte
genérica: la aserción (`hp <= 10`) rechazaba un resultado real y válido del motor de daño
(Tackle nv5, atk 17 vs def 9 → daño base 5; con crítico real 1/16 el daño sube a 8 o 9, y 9 deja
el HP en exactamente 10). Cota corregida a `hp < 10` tras calcular el rango real posible con la
fórmula de `damage.go` (no adivinado) — 100 corridas seguidas + toda la suite del paquete 3
veces, verde.

Nota sobre `go.mod`: ya no tiene directivas `replace` (se quitaron; `go mod tidy` resuelve
todo contra el proxy oficial de Go sin problema en este entorno). `go 1.24` es el mínimo
requerido por `golang-jwt/jwt/v5` y `redis/go-redis/v9`. Logging vía `log/slog` (nivel
configurable con `LOG_LEVEL=debug|info|warn|error`), no `log.Printf`.

## 6b. Tests de integración de Go

Requieren una base de Postgres real (no hay mocks para trade/auth/battlesession — son los
módulos más sensibles del proyecto). Crear una vez:

```powershell
$env:PGPASSWORD = "pokemon"
& postgresql-16.5\pgsql\bin\psql.exe -U pokemon -h localhost -p 5432 -d postgres -c "CREATE DATABASE pokemon_online_test OWNER pokemon;"
& postgresql-16.5\pgsql\bin\psql.exe -U pokemon -h localhost -p 5432 -d pokemon_online_test -f database\migrations\0001_init_schema.sql
```

**Importante**: en un entorno sin Docker (Postgres nativo, ver sección 3), las migraciones
posteriores a la `0001` NO se aplican solas — hay que correr cada `database/migrations/000X_*.sql`
a mano contra AMBAS bases (`pokemon_online` la real, `pokemon_online_test` la de tests) cada vez
que se agrega una. Olvidarse de una de las dos hace que los tests pasen y el servidor real falle
(o viceversa) sin ningún aviso — pasó de verdad con la `0007_pokemon_battle_fields.sql`.

Correr:
```powershell
cd server
$env:TEST_DATABASE_URL = "postgres://pokemon:pokemon@localhost:5432/pokemon_online_test?sslmode=disable"
go test ./... -v
```
Si no hay conexión a `TEST_DATABASE_URL` (ni al default), los tests se saltan (`t.Skip`) en
vez de fallar — no rompen `go build`/`go vet` en un entorno sin Postgres. Paquetes con test hoy:
`trade`, `auth`, `battle` (unitario puro, no necesita Postgres), `battlesession`, `wildencounter`,
`pokemon` (curvas de experiencia reales + aprendizaje de movimientos, ver sección 6), `social`
(amigos/grupos/gremios), `market`, `chat` (unitario puro, con fakes — no necesita Postgres), `ws`
(Hub, unitario puro). **Sin test todavía**: `world` (router.go, 1194 líneas de wiring/dispatch —
cubierto indirectamente por los ~15 smoke tests de `scripts/*.js`, que sí ejercitan el router de
punta a punta contra un servidor real; escribir un test Go dedicado exigiría mockear ~8 servicios
distintos para una capa que es casi toda plomería, no lógica propia — no se consideró buen
retorno de inversión esta sesión).

**Si corrés el binario compilado (`server.exe`) en vez de `go run`**: recompilarlo y
reiniciar el proceso después de tocar código de servidor — no se actualiza solo. Es fácil
terminar probando en vivo contra una build vieja sin darse cuenta (pasó en esta misma sesión).

**Migraciones**: ya no hace falta aplicarlas a mano — `powershell -File .\scripts\apply-migrations.ps1`
las aplica (con tabla de control `schema_migrations`, idempotente) contra `pokemon_online` Y
`pokemon_online_test` en una sola corrida. `-WhatIf` para ver qué haría sin tocar nada. Esto
reemplaza el proceso 100% manual que ya había causado un bug real (olvidarse de aplicar una
migración a una de las dos bases, ver sección 6).

**Backups de la base**: `powershell -File .\scripts\backup-database.ps1` vuelca `pokemon_online`
completa (pg_dump, formato plano) a `database\backups\` con timestamp, y borra automáticamente
los backups más viejos si hay más de 14 (parámetro `-KeepLast`) — pensado para correr como tarea
diaria de Windows Task Scheduler (comando de ejemplo en el propio script; no se programa solo,
es una decisión de quien hostee el servidor). `scripts\restore-database.ps1 -BackupFile <archivo>`
restaura (dropea y recrea la base entera — pide confirmación escrita salvo `-Force`). Probado de
punta a punta esta sesión: backup real de `pokemon_online` (407 KB, todas las tablas con datos
reales) y restauración completa contra `pokemon_online_test` sin errores, con los tests de Go
corriendo verdes contra la base restaurada después.

## 7. Siguientes pasos recomendados (en orden)

Los puntos que estaban acá (UI real del cliente, chat/amigos/grupos/trading en el cliente,
snapshot inicial de jugadores, mecanismo de captura servidor-autoritativo, cobertura de tests
para social/market/chat/ws, migraciones automatizadas, curvas de experiencia reales +
aprendizaje de movimientos, empaquetado self-contained del cliente, pantalla real de "reemplazar
movimiento", verificación de email, encuentros de agua/pesca/rock-smash, señal de RAM del
encuentro salvaje, launcher y updater, backups de la base + recuperación de contraseña) **ya
están hechos** — ver sección 6 y `client-engine/README.md`. Lo que sigue pendiente, en orden
aproximado de impacto:

1. **Activar SMTP real para la verificación de email y recuperación de contraseña** (la lógica
   de ambas ya está completa y probada, ver sección 6): hoy usa el fallback de consola porque
   este entorno no tiene credenciales de correo configuradas. Con Gmail: crear (o usar) una
   cuenta, generar una "contraseña de aplicación" en
   https://myaccount.google.com/apppasswords, y setear `SMTP_HOST=smtp.gmail.com`,
   `SMTP_PORT=587`, `SMTP_USERNAME=<tu cuenta>@gmail.com`, `SMTP_PASSWORD=<la contraseña de
   aplicación>`, `SMTP_FROM=<tu cuenta>@gmail.com` antes de levantar `server.exe`.
2. Multi-ROM más allá de Emerald ES/US: agregar `firered_us.json`, `ruby_us.json`, etc., una
   vez que alguien tenga esas ROMs — **bloqueado de verdad, no solo pendiente**: no hay ninguna
   otra ROM en este repo/entorno, y este proyecto tiene la regla dura de nunca adivinar
   direcciones de memoria (ver `gba_memory_scanning_method` en memoria), así que no se puede
   avanzar sin una ROM real para validar contra ella jugando de verdad. Auditoría de qué tan
   listo está el motor para cuando llegue esa ROM (2026-07-27): la mayor parte YA es agnóstica
   de ROM (`RomCatalog.Discover`, `MemoryMapConfig`, `IMemoryAdapter`, `Gen3Codec` — confirmado
   grepeando todo `client-engine/` en busca de "Emerald"/"BPES"/"BPEE" hardcodeado, solo
   aparecen en comentarios descriptivos). Dos gaps reales encontrados, ninguno arreglado
   todavía (arreglarlos sin una segunda ROM para probar sería trabajo no verificable a ciegas):
   - `client-engine/RomLoader/StarterCatalog.cs` y su gemelo `server/internal/pokemon/starters.go`
     tienen los 3 iniciales de Emerald (Treecko/Torchic/Mudkip) totalmente hardcodeados —
     duplicados a mano en C# y Go porque para 3 entradas no se justificaba generarlos (decisión
     ya tomada en una sesión anterior, ver el comentario en `starters.go`). Otra ROM con
     iniciales distintos (FireRed: Bulbasaur/Charmander/Squirtle) necesitaría esto
     data-driven por ROM, no hardcodeado.
   - `client-engine/ClientApp/Program.cs` (`spritesDir` default) apunta al checkout de
     `Pokemon Esmeralda/pokeemerald-master` fijo — overridable con `--sprites-dir`, pero no se
     resuelve solo por ROM elegida (una ROM de otro juego necesitaría su propio checkout de
     sprites, ej. un decompilation de FireRed).

## 8. Notas importantes

- El proyecto nunca aloja ni distribuye ROMs ni assets de Nintendo/Game Freak. El usuario
  aporta su propia ROM legalmente obtenida; solo se lee su memoria en tiempo de ejecución local.
- La contraseña se hashea con bcrypt antes de tocar la base de datos (`internal/auth/auth.go`).
  Nunca loguear ni almacenar contraseñas en texto plano.
- El módulo de trading (`internal/trade/trade.go`) es el más sensible del proyecto: cualquier
  cambio ahí debe ir acompañado de pruebas que verifiquen que no es posible duplicar un Pokémon
  (doble confirmación simultánea, desconexión a mitad de transacción, etc.).
- Las batallas PvP (`internal/battlesession`) **nunca** otorgan experiencia ni suben de nivel —
  decisión deliberada (evitar levelear gratis peleando contra un aliado sin riesgo real): solo
  se persiste el HP. Si en algún momento se agrega experiencia a PvP, tiene que llevar algún
  límite/validación (igual que el `battle_result` de PvE ya valida que los deltas de dinero/
  experiencia sean razonables) — no un otorgamiento libre.
