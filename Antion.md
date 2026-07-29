# Antion — la guía/biblia del proyecto

Esta es la guía completa de **Pokémon Online** (nombre de trabajo del proyecto: Antion) para
cualquier IA (u otra sesión) que vaya a trabajar acá. `CLAUDE.md` (que Claude Code carga solo,
automáticamente, en cualquier sesión) apunta directo a este archivo — leelo entero antes de
tocar código. No es documentación decorativa — cada sección existe porque algo salió mal, se
perdió tiempo real, o el usuario corrigió un enfoque específico en una sesión anterior.

## 0. Qué es este proyecto, en una frase

Un MMO de Pokémon real: servidor multijugador propio en Go + un cliente en C#/.NET que corre
una ROM de verdad de Pokémon Esmeralda dentro de un emulador embebido (mGBA vía libretro) y
lee/escribe su RAM en vivo — no es un clon reimplementado desde cero, es la ROM real siendo
"conectada" a un mundo compartido servidor-autoritativo. Inspirado en PokeMMO, decisión tomada
explícitamente por el usuario tras evaluar alternativas (ver sección 3).

**Antes de asumir que algo "no está implementado": leé `README.md` completo** (secciones 6 y
6b especialmente) — es un changelog vivo, detallado, con fecha, de todo lo que se hizo y se
verificó. Este archivo no lo duplica; lo complementa con el "por qué" y las reglas que no están
ahí.

## 1. Reglas de oro (no negociables, violarlas ya causó bugs reales)

1. **Nunca adivinar direcciones de memoria RAM de la ROM.** Toda dirección en
   `memory-maps/*.json` fue validada empíricamente (volcando RAM real, diffeando estados
   conocidos) — nunca copiada de una fuente externa sin confirmar en vivo contra este proyecto.
   Metodología completa y reutilizable: ver sección 5. Si necesitás una dirección nueva, seguí
   ese método — no la busques en documentación de terceros y la des por buena sin probarla.

2. **Arquitecto primero, no implementador.** Antes de agregar cualquier feature nueva,
   preguntate: ¿esto necesita saber algo específico de ROM (offsets, layouts, nombres de mapa,
   convenciones de sprite)? Si sí, va en `RomLoader`, expuesto al motor vía una interfaz
   abstracta (`IMemoryAdapter`) — nunca inline en `ClientApp`/`LibretroCore`. Si no es obvio en
   qué capa va algo (motor vs. servidor vs. cliente vs. `RomLoader` vs. `Launcher`), decilo y
   proponé el trade-off en vez de elegir en silencio el camino más corto.

3. **Sin sobreingeniería.** Esto corre para un puñado de amigos, no para producción pública a
   escala. Preferí lo simple y real por sobre lo "correcto en teoría": sin transacciones DB en
   ningún lado (ni falta hace — ver `TryDebitMoney` en `internal/character` para el único caso
   donde la atomicidad sí importó, resuelto con un solo `UPDATE...WHERE`, no con una transacción
   explícita), sin mocks en los tests de Go (todos corren contra Postgres real), sin
   arquitecturas especulativas para casos que no existen todavía.

4. **Fail-open para infraestructura opcional.** Si Redis no está disponible, el rate-limit de
   chat simplemente no limita (no bloquea el arranque del servidor). Si SMTP no está
   configurado, los emails de verificación/reset se loguean en consola en vez de mandarse. Este
   patrón es intencional y se repite: un servicio opcional caído nunca debe tumbar el flujo
   principal.

5. **El servidor decide todo lo que importa.** Daño de batalla, captura, precios, quién ganó un
   trade — todo server-autoritativo. El cliente refleja estado y manda intenciones, nunca
   decide un resultado. `BattleScreen.cs` es un ejemplo canónico: dibuja lo que el servidor le
   manda, nunca calcula daño localmente.

6. **Nunca se descarga, incluye ni distribuye ninguna ROM.** El Launcher detecta/valida ROMs
   que el usuario ya tiene (por hash SHA1 contra `memory-maps/*.json`), nunca ofrece bajar una,
   nunca enlaza a dónde conseguirla. Esto es una restricción legal explícita del usuario, no
   una preferencia técnica — no la relajes ni con buenas intenciones.

## 2. Estructura del proyecto (mapa rápido)

```
server/                    Go — servidor autoritativo (auth, mundo, chat, batalla, mercado...)
  cmd/server/main.go        entrypoint HTTP+WS, wiring de todos los servicios
  cmd/gendata/               genera data/pokemon/*.json desde el código fuente real de pokeemerald
  internal/*/                un paquete por dominio (battle, battlesession, wildencounter,
                              inventory, character, market, trade, social, ws, world/router.go...)
client-engine/              C#/.NET — todo lo que corre en la máquina del jugador
  ClientApp/                 el juego en sí: ventana Win32 + Direct3D 11, emulador embebido,
                              login/registro/batalla/chat/panel social, todo dibujado a mano
                              (sin librería de UI — Renderer.AddText/AddRect)
  RomLoader/                  abstracción ROM-agnóstica (IMemoryAdapter, MemoryMapConfig,
                              Gen3Codec) — la ÚNICA capa que sabe leer/escribir RAM de una ROM
                              específica; el motor (ClientApp) nunca la toca directo
  LibretroCore/                wrapper genérico de hardware GBA (mGBA vía libretro) — no sabe
                              nada de Pokémon, solo "emulador GBA"
  Launcher/                    launcher WPF/MVVM independiente (ver sección 4) — detecta/valida
                              ROM, actualiza el cliente, muestra noticias/estado del servidor
  Launcher.Tests/, ClientApp.Tests/   xUnit — únicos tests del lado C# (agregados 2026-07-29,
                              antes CERO cobertura acá; el servidor Go sí tenía mucha desde antes)
client-stub/Protocol/        contrato de protocolo compartido (mismo Messages.cs que usa un
                              hipotético cliente Godot — no duplicar tipos)
common/protocol/PROTOCOL.md  documentación del protocolo WS mensaje por mensaje
memory-maps/*.json           direcciones de RAM validadas por ROM (emerald_es, emerald_us)
data/pokemon/                species.json/moves.json/encounters.json/maps.json/news.json —
                              generados por gendata desde pokeemerald real, NO transcritos a mano
database/migrations/         SQL numerado (0001, 0002...) — ver sección 6, hay una trampa acá
Pokemon Esmeralda/            checkout local de pokeemerald (decompilation real) — fuente de
                              verdad para CUALQUIER dato de Gen3 (IDs, fórmulas, offsets de
                              struct, precios de items) — nunca "recordar" estos valores, grepear
                              este checkout siempre que haga falta un dato real de Emerald
postgresql-16.5/, go1.26.5/   runtimes PORTABLES (no instalados en el sistema) — ver sección 6
postgres_data/                los datos REALES de Postgres — es una carpeta, no un contenedor
RESTAURAR-PROYECTO.md         cómo levantar todo esto en una máquina nueva desde cero
```

## 3. Decisiones de arquitectura ya tomadas (no las reabras sin motivo nuevo)

- **PokeMMO, no motor genérico.** Se evaluó reescribir esto como un engine genérico tipo AAA
  (ECS, físicas, editor) y se descartó explícitamente — el diseño correcto es "cliente renderiza
  una ROM real emulada, servidor solo arbitra estado compartido", no un motor que posea la
  simulación. No propongas volver a esa idea salvo que el usuario la traiga de nuevo.
- **El motor NO renderiza el mundo nativamente.** Sigue delegando mapas/tiles/NPCs al emulador
  real (mGBA). `RomLoader` se limita a LEER/ESCRIBIR memoria en vivo (posición, equipo, flags) —
  nunca a extraer/parsear assets de la ROM para dibujarlos aparte. Esto fue una decisión
  explícita rechazando la alternativa "más ambiciosa" de que el motor parseara Map/Tileset/NPC
  de la ROM y dibujara todo nativo.
- **PvP nunca da experiencia/niveles.** Deliberado, para que no se pueda farmear XP con
  batallas amistosas sin riesgo. Solo el modo salvaje (dentro del emulador) y la historia dan
  experiencia real.
- **Multi-ROM: arquitectura lista, contenido bloqueado.** El motor ya es agnóstico de ROM
  (`RomCatalog`, `MemoryMapConfig`, `IMemoryAdapter`, `Gen3Codec`) pero agregar una ROM nueva
  (FireRed, Ruby...) requiere el proceso empírico completo de la sección 5 contra esa ROM
  específica — nunca asumas que los offsets de Emerald sirven para otra ROM sin probarlo. Dos
  gaps conocidos y sin arreglar todavía si llega una ROM nueva: `StarterCatalog.cs`/
  `starters.go` hardcodean los iniciales de Emerald, y `spritesDir` apunta fijo al checkout de
  Emerald.

## 4. El Launcher (reescrito 2026-07-28/29, WPF — antes era WinForms)

Proyecto separado (`client-engine/Launcher`), MVVM (`ViewModels/`, `Services/`, `Converters/`,
`Localization/`, `Themes/`), sin depender del motor del juego. Detecta/valida ROM por SHA1
(nunca la descarga), pantalla principal con noticias/estado del servidor/ping/jugadores
conectados (refrescado cada 15s), botón JUGAR grande, selector de idioma ES/EN, "recordar
usuario" (solo precarga el campo, nunca guarda contraseña). `ClientApp` recibe
`--rom/--memory-map/--rom-id` ya resueltos por argumento — no descubre ROM por su cuenta cuando
viene del Launcher (si corrés `ClientApp.exe` suelto sin esos argumentos, sí vuelve a su
catálogo multi-ROM viejo, por compatibilidad de desarrollo).

Nuevos endpoints de servidor que esto necesitó: `GET /server-status` (jugadores conectados,
`hub.Count()`), `GET /news` (lee `data/news.json` tal cual, editable a mano, sin base de
datos/CMS — mismo criterio "simple" del punto 1.3).

## 5. Metodología para validar direcciones de RAM (cuando haga falta encontrar una nueva)

**Nunca empieces por "buscar el puntero y asumir el offset del struct conocido"** — falló dos
veces (ver `gba_memory_scanning_method` abajo). El método que sí funciona, en orden:

1. **Restringí el espacio de búsqueda antes de diffear.** IWRAM (32KB, `--dump-iwram`) para
   globals de estado del motor (ej. `gMain.callback1/2`); una región ya conocida de EWRAM
   (ej. `0x02020000-0x02030000`, donde ya viven otros campos validados) para datos de
   save/posición — la región baja de EWRAM (`0x02000000-0x0201FFFF`) es puro ruido de gráficos/
   audio/heap, diffear ahí sin filtro no sirve.
2. **Tomá múltiples muestras independientes**, no un solo par antes/después — mínimo 2-3 en
   cada estado, en momentos distintos. Un candidato solo cuenta si reproduce el mismo valor
   exacto en TODAS las muestras del mismo grupo.
3. **Si conocés la forma de un struct vecino** (ej. "este campo siempre está seguido por otro
   que es constante"), filtrá por esa PAREJA, no por el valor suelto — corta el ruido
   drásticamente (un caso real: 323 candidatos → exactamente 1).
4. **Confirmá contra el código fuente real** (`Pokemon Esmeralda/pokeemerald-master/`) — nombres
   de constantes, layout de structs, fórmulas — nunca "recordado" de memoria genérica de Gen3.
5. Documentá el hallazgo en el propio `memory-maps/*.json` (campo `_validation`), con la
   metodología exacta usada — el próximo que lea el JSON tiene que poder confiar en el dato sin
   tener que repetir el trabajo.

`SaveBlock1Ptr`/`SaveBlock2Ptr` (dinero, equipo, posición, flags) son **punteros que se realocan
en cualquier momento** (confirmado: cambian solo con abrir un menú) — siempre indirección de
puntero releída en cada acceso, NUNCA una dirección fija cacheada. `gPlayerParty`
(`0x020244EC`) en cambio es un global estático fijo (no heap) — no confundir los dos patrones.

## 6. Entorno de desarrollo (cosas específicas de ESTA máquina/sesión)

- **Postgres NO corre en Docker acá**, aunque `docker-compose.yml` exista — corre nativo,
  portable, desde `postgresql-16.5/pgsql/bin/`, con datos reales en `postgres_data/` (una
  carpeta, no un volumen Docker). Usuario/clave: `pokemon`/`pokemon`, base `pokemon_online`
  (+ `pokemon_online_test` para tests de integración). `docker-entrypoint-initdb.d` nunca corre
  acá — **las migraciones nuevas hay que aplicarlas a mano** (`scripts\apply-migrations.ps1`) a
  **las dos bases** (runtime y test) — aplicar a una y olvidarse de la otra ya pasó y causó
  tests que mentían.
- **`server/server.exe` es un binario prebuilt**, no algo que corra con `go run` en el uso
  normal. Después de tocar código de servidor: `go build -o server.exe ./cmd/server`, parar el
  proceso viejo (`Stop-Process -Name server -Force`), levantar el nuevo — si no, seguís
  probando contra la build vieja en silencio. (`scripts/start-everything.ps1` usa `go run`
  directo, sirve para pruebas rápidas sin generar el .exe.)
- **`database/migrations/` es la carpeta real** (numeración de 4 dígitos). Existió una vez una
  `server/migrations/` paralela y falsa (numeración de 3 dígitos, nunca aplicada, con archivos
  que directamente chocaban con el esquema real) — fue un error, se borró. Si alguna vez ves
  dos carpetas de migraciones, es una señal de alarma, no una decisión válida.
- **Esta sesión corre dentro de un Terminal Server con múltiples sesiones RDP concurrentes.**
  Los discos que el usuario conecta/redirige desde SU sesión interactiva (`rdp-tcp#1`) **no son
  visibles** desde el contexto donde corren las herramientas de shell — son un recurso
  redirigido de esa sesión específica, no un volumen real de Windows (`Get-Volume`/
  `Get-CimInstance Win32_LogicalDisk` no los detecta). Si el usuario dice "ya conecté el disco y
  lo veo en el Explorador" pero vos no lo ves ni con `ls`/`dir` directo a esa letra: es esto, no
  un error tuyo. Solución práctica: preparar todo en `C:\` y darle al usuario el comando exacto
  para que ÉL lo copie desde su propia sesión — no hay forma de escribir ahí directamente desde
  las herramientas de shell.
- **GDI screen capture no funciona en este sandbox** (`Graphics.CopyFromScreen` falla con
  "Controlador no válido" aunque la ventana exista de verdad). Para ver la pantalla del juego:
  `ClientApp` tiene su propia captura de backbuffer (F1 → `dumps/shot_*.bmp`, convertir a PNG
  con `System.Drawing.Image.FromFile` antes de leerlo). Para manejar el juego sin mouse:
  `PostMessage` (WM_KEYDOWN/UP) directo al HWND por `user32.dll` — más confiable que
  `keybd_event`, no necesita foco de ventana. `--dev-boot --load-state dumps/savestate.bin`
  salta la intro completa (login automático `client_dev`/`clientdev123`, aunque esa cuenta NO
  tiene ningún Pokémon en la base — no sirve para probar cosas que necesiten equipo real sin
  agregarle uno a mano primero, ej. con F8/`--test-write`).
- **PowerShell en esta sesión es Windows PowerShell 5.1**, no pwsh moderno — sin `&&`/`||`, sin
  ternario, sin here-strings con backtick. Ver la guía de la propia herramienta PowerShell para
  la lista completa de sintaxis que NO anda acá.
- **`robocopy` devuelve código de salida 1 en un copiado EXITOSO** (es una máscara de bits, no
  un código de error convencional) — si una copia grande "falla" con código 1 pero el log dice
  0 errores, fue exitosa; no lo reintentes pensando que rompió algo.
- **`dotnet build` falla si el .exe de salida está corriendo** — parar el proceso
  (`Stop-Process -Name <Proyecto>`) antes de recompilar un ejecutable que quedó abierto de una
  prueba anterior.

## 7. Cómo levantar/restaurar el proyecto

Ver **`RESTAURAR-PROYECTO.md`** — guía paso a paso completa (requisitos, cómo levantar Postgres
con datos reales, plan B para restaurar desde un dump SQL si `postgres_data` no está disponible,
tabla de nombres/contraseñas/puertos). `scripts\check-prerequisites.ps1` audita qué falta en una
máquina nueva antes de perder tiempo con los pasos manuales.

## 8. Convenciones de código

- Comentarios en español, solo para explicar el **PORQUÉ** no obvio (una decisión, un bug real
  que motivó el código, una restricción externa) — nunca describir QUÉ hace el código si ya es
  legible por sí solo. Esto es la norma real de todo el codebase existente, no una preferencia
  nueva — segui el mismo tono/densidad que ya vas a encontrar en cualquier archivo.
- Nombres de tabla/columna/constante SIEMPRE verificados contra la fuente real antes de usarlos
  (`database/migrations/0001_init_schema.sql` para el esquema, `Pokemon Esmeralda/
  pokeemerald-master/` para cualquier dato de Gen3) — nunca "recordados" de una sesión anterior
  sin re-confirmar si pasó tiempo.
- Tests de Go: siempre contra Postgres real (`testDB(t)` con `TEST_DATABASE_URL` o el default
  `pokemon_online_test`), nunca mocks — es la convención ya establecida en cada paquete
  (`internal/market`, `internal/battlesession`, `internal/character`, etc.).
- Antes de escribir un test nuevo para algo con aleatoriedad real (daño de batalla, captura):
  calculá el rango REAL posible con la fórmula del código, no una cota "que suene razonable" —
  un test así causó un flaky real (`TestItem_HealAndConsumesInventory`, cota `<= 10` rechazaba
  un resultado real y válido de crítico) que costó tiempo de sesiones futuras hasta que alguien
  hizo la cuenta exacta.

## 9. Preferencias de trabajo del usuario

- Prefiere que evalúes arquitectura antes de implementar (ver punto 1.2) — no asumas la salida
  más rápida si compromete capas.
- Le gusta verificación empírica real, no "debería funcionar" — cuando algo es verificable en
  vivo (batalla, encuentro salvaje, endpoint), hacelo y mostrá la evidencia (captura, log,
  smoke test), no solo "compila y la lógica parece correcta".
- Cuando pide "seguir con todo" o "continuá", interpretalo como autorización para trabajar
  varias tareas seguidas sin pedir confirmación en cada paso — pero seguí pausando antes de
  acciones destructivas/irreversibles reales (parar servicios en uso, borrar datos, hacer
  push/force) y explicando decisiones de diseño no triviales en una o dos frases al tomarlas.
- Este proyecto es personal/para un grupo de amigos, no producción pública — calibrá el nivel
  de rigor de ingeniería a eso (ver punto 1.3), no le agregues robustez de nivel empresarial que
  nadie pidió.

## 10. Dónde seguir leyendo

- `README.md` — estado detallado y verificado de cada feature, con fecha. **La fuente de verdad
  sobre "qué está hecho" — revisala antes de asumir que algo falta.**
- `client-engine/README.md` — detalle específico del cliente/motor (arquitectura de renderer,
  pipeline de input, etc.)
- `common/protocol/PROTOCOL.md` — cada mensaje del protocolo WebSocket, documentado.
- `RESTAURAR-PROYECTO.md` — cómo levantar todo de cero.
- Sección 7 de `README.md` — lista priorizada de qué falta de verdad (multi-ROM bloqueado sin
  segunda ROM, SMTP real pendiente de credenciales del usuario, y lo que se vaya agregando).
