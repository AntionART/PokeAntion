# Cliente — motor propio en C#/.NET (Fase D)

Decisión de diseño (2026-07-14): el cliente **no** usa Godot. Es un motor propio en C#/.NET,
construido desde cero (Win32 + Direct3D/OpenGL, sin MonoGame), donde la pantalla del juego
**es el framebuffer real de la ROM emulada** (como PokeMMO) con overlay de UI propio (chat,
otros jugadores, trading) dibujado en la misma pasada de render.

Esto descarta dos alternativas consideradas:
- **Embeber la ventana nativa de mGBA** (`SetParent` de Win32): tiene problemas serios de
  "airspace" para dibujar overlays encima de un HWND ajeno en WinForms/WPF.
- **mGBA standalone + Lua + socket**: solo resuelve la lectura de datos, no la imagen; seguiría
  habiendo dos ventanas separadas.

En su lugar: **libretro + P/Invoke**. Se carga `mgba_libretro.dll` (el core de mGBA para
libretro) directo en el proceso del cliente. Esto da acceso al framebuffer como buffer de
píxeles (para blitear en nuestra propia textura) *y* a la RAM de la ROM (para el adaptador de
memoria) sin depender de una segunda ventana ni de scripting externo.

### El flujo de un jugador de la ROM NO es el flujo del producto final

Decisión de producto (2026-07-15): como esto es multijugador con cuentas propias en el
servidor (`/register`, JWT — ver `server/`), el cliente **no** va a pasar al jugador por la
introducción/tutorial de un jugador de la ROM (logo animado, "PARTIDA NUEVA", elegir nombre,
cinemática del profesor Birch). En su lugar, el motor propio va a tener sus propias pantallas
de **login/creación de cuenta**, **selección de ROM** (entre las que el servidor soporte, ver
`memory-maps/`) y **configuración**, y recién ahí cargar la ROM ya "saltada" directo al mundo
(vía un save state inicial por ROM, o inyectando el estado del personaje). Esto es trabajo de
Fase D.4/F, todavía no implementado — hoy `ClientApp` sigue arrancando por el flujo completo
de la ROM porque así es como se validó D.1-D.3 (ver más abajo).

## D.1 — Prueba de concepto: cargar el core y leer RAM (✅ hecho y verificado)

`LibretroPoc/` es un proyecto de consola que:
1. Carga `mgba_libretro.dll` vía P/Invoke (ver `Libretro.cs`, ABI mínima de `libretro.h`).
2. Le pasa la ROM real del repo y corre 180 frames.
3. Lee su memoria y confirma que hay datos reales (no ceros) de la partida corriendo.

Correrlo:
```powershell
cd client-engine/LibretroPoc
dotnet run
```

### Hallazgo importante: el id legado `RETRO_MEMORY_SYSTEM_RAM` NO es confiable en este core

`retro_get_memory_data(RETRO_MEMORY_SYSTEM_RAM)` es la forma "vieja"/simple de pedirle RAM a
un core libretro. En `mgba_libretro.dll` (0.11-dev), esto devuelve un puntero que en la
práctica correspondía a EWRAM, pero con un **tamaño reportado incorrecto** (32768 bytes,
que es el tamaño de IWRAM, no los 262144 bytes reales de EWRAM). Confiar en ese id habría
producido lecturas truncadas/corruptas de forma silenciosa.

La vía correcta y confiable es manejar el comando `RETRO_ENVIRONMENT_SET_MEMORY_MAPS`
(`36 | RETRO_ENVIRONMENT_EXPERIMENTAL`) en el callback de entorno: el core lo invoca durante
`retro_load_game` para anunciar **todas** sus regiones de memoria con nombre. Confirmado
contra la ROM real (`BPES`, Pokémon Esmeralda español), el core anuncia 11 regiones; la que
nos interesa para el adaptador de memoria es:

```
start=0x02000000  len=0x40000 (262144 bytes)  flags=RETRO_MEMDESC_SYSTEM_RAM
```

Esto coincide exactamente con el rango `0x0203xxxx` que ya usa (como placeholder)
`memory-maps/emerald_us.json` — confirma que ese es el enfoque correcto para direcciones de
juego (player, save block, equipo), una vez validadas para la ROM real.

## D.2 — Ventana real con Direct3D 11 (✅ hecho y verificado)

`ClientApp/` es la primera versión con ventana de verdad: `Win32Window.cs` (RegisterClassEx +
CreateWindowEx + message pump a mano, sin WinForms/WPF) y `Renderer.cs` (Direct3D 11 vía
Vortice.Windows — device, swapchain, y un triángulo fullscreen sin vertex buffer que dibuja
el framebuffer del emulador como textura). El framebuffer llega en RGB565 (confirmado en
D.1) y se convierte a BGRA8 en CPU antes de subirlo a una textura dinámica.

Correrlo:
```powershell
cd client-engine/ClientApp
dotnet run
```

**Verificado con evidencia real** (no solo "compiló"): como este entorno de desarrollo no
tiene captura de pantalla disponible (sesión RDP sin driver de video), `Renderer.CaptureToFile`
vuelca el backbuffer real (no una captura de escritorio) a un BMP. El volcado a los ~10s de
boot muestra, sin ningún artefacto de color ni de alineación, la animación de introducción
real de Pokémon Esmeralda (el iris de hojas/gotas de rocío antes del título) — confirma que
la conversión RGB565→BGRA8, el manejo del row pitch, y el pipeline de shaders están
correctos. Para reproducir el volcado de diagnóstico:
```powershell
dotnet run -- "<ruta-a-la-rom>.gba" --dump-frame salida.bmp 600
```

Bugs encontrados y corregidos en el camino:
- **`WNDCLASSEX` sin `CharSet.Unicode` en el struct**: los campos `string` se marshaleaban
  como ANSI mientras se llamaba a `RegisterClassExW`/`CreateWindowExW` (las versiones wide),
  desalineando el nombre de clase entre el registro y la creación de ventana — `CreateWindowEx`
  fallaba. Al no tener `SetLastError = true` en los `[DllImport]`, además, `GetLastWin32Error()`
  devolvía 0 (ninguna pista real del error); se corrigieron ambas cosas.

Decisiones de esta primera versión (deliberadamente simplificadas, revisar antes de Fase F):
- Escala fija 4x (960x640), sin soporte de resize todavía.
- Sampling "point" (sin blur), para look pixel-art nítido.
- Sin audio (el callback de audio de libretro es no-op en `MgbaCore`).
- Bucle principal sin cap de FPS explícito — `Present(1, ...)` sincroniza contra vsync, que
  en la práctica alcanza para verse fluido, pero no es el ritmo exacto (~59.7275fps) de GBA.

**Input real y herramientas de desarrollo agregadas después de la verificación inicial**
(jugado a mano por el usuario, no solo probado por el agente):
- Teclado -> joypad de GBA: flechas = D-Pad, Z = A, X = B, Enter = Start, Backspace = Select,
  A/S = L/R (convención clásica de emuladores tipo VBA).
- **Tab** (mantenido) = turbo: corre 6 frames de emulación por cada frame renderizado, para
  acelerar diálogos/desplazamiento durante el desarrollo sin depender de la opción de
  velocidad de texto del juego.
- **F1** = vuelca EWRAM cruda (256KB) + una captura a `dumps/`, con timestamp. Es la
  herramienta que hizo posible D.3 (ver abajo): pararse, F1, moverse, F1, diffear.
- **F3** = guarda un save state de libretro a `dumps/savestate.bin`; `--load-state <path>` lo
  retoma al arrancar, para no tener que rejugar la introducción de la ROM en cada reinicio
  durante el desarrollo.

## D.3 — Direcciones reales de posición del jugador (✅ hecho y verificado para BPES)

Encontradas **jugando de verdad** (el agente no tiene captura de pantalla en este entorno de
desarrollo, así que el usuario jugó y usó F1 para volcar memoria en los puntos exactos que
hacían falta) y confirmadas con dos pruebas de movimiento controlado e independientes:

| Campo | Dirección | Evidencia |
|---|---|---|
| `player.pos_x` | `0x02025A30` (u16 LE) | Sin cambios en un movimiento vertical puro; `+5` exacto tras 5 pasos a la derecha |
| `player.pos_y` | `0x02025A32` (u16 LE) | `+4` tras el movimiento vertical; sin cambios en el movimiento horizontal puro |

Documentado con el detalle completo (incluida la hipótesis sobre la estructura de datos que
las contiene) en `memory-maps/emerald_es.json` — **archivo nuevo**, específico para la ROM
española (`BPES`), separado de `emerald_us.json` (placeholder, para `BPEE`, todavía sin
conseguir esa ROM para validar).

Metodología (repetible para cualquier ROM soportada, y para el resto de los campos que
todavía faltan — mapa actual, equipo, dinero):
1. Llegar a un estado con el personaje controlable (`F3` para no repetir esto cada vez).
2. Pararse quieto, `F1` (primer volcado).
3. Moverse un número exacto de tiles en **una sola dirección**.
4. Pararse, `F1` (segundo volcado).
5. Diffear los dos `.bin` byte a byte; buscar deltas que coincidan exactamente con los tiles
   recorridos. Repetir con una dirección distinta para separar X de Y (y descartar falsos
   positivos — hubo varios candidatos con delta +4 en el primer test que resultaron ser ruido
   de buffers de tilemap, no posición).

**Pendiente**: `map_bank`/`map_number` (falta una prueba cruzando de mapa), y todo
`save_block` (equipo, dinero, PC) — mismo método, no hecho todavía.

## D.4 — Contrato `IMemoryAdapter` (✅ hecho y verificado para posición del jugador)

`RomLoader/MemoryAdapter.cs`: `IMemoryAdapter.GetPlayerPosition()`, implementado por
`GbaMemoryAdapter`, que lee un `memory-maps/*.json` (deserializado a `MemoryMapConfig`) y
traduce sus direcciones a lecturas reales sobre el `MgbaCore.FindEwram()` de D.1. El resto
del cliente debería hablar solo con `IMemoryAdapter`, nunca hardcodear una dirección —
así cambiar de ROM es swapear el JSON, no tocar código (Fase H, multi-ROM).

Verificado contra el save state real capturado con F3 durante las pruebas de D.3 (comando
`POS` de `Explorer/`, que instancia `GbaMemoryAdapter` de verdad, no un mock):
```
dotnet run -- rom.gba script.txt --load-state dumps/savestate.bin
# script.txt: POS ../../memory-maps/emerald_es.json
# -> POS (emerald_es) -> x=11 y=11   (coincide exacto con lo esperado)
```

Todavía solo expone posición (lo único validado en D.3); `GetTeam()`, `GetMoney()`, etc. se
suman a medida que se validen las direcciones correspondientes con la misma metodología.

## Fase RomLoader — Motor desacoplado de la ROM (✅ fases 1-5 hechas, ver memory-maps del proyecto)

El motor no sabe nada de Pokémon Esmeralda: todo lo específico de una ROM (direcciones de
memoria, heurística de sprite del jugador, rom_id) vive en `RomLoader/` (proyecto separado de
`LibretroCore`, que sigue siendo hardware GBA genérico) y en `memory-maps/*.json`. Agregar una
ROM nueva es agregar un `.gba` + su `memory-maps/*.json` al lado — cero cambios de código en
`ClientApp`/`RomLoader`, si la abstracción realmente cerró (ver Fase 6, pendiente de una
segunda ROM real para probarlo).

`player.sprite` en el memory-map declara cómo identificar el sprite del jugador en OAM (ancho,
alto, índice de paleta, centro de cámara esperado) — antes esto estaba hardcodeado en
`Program.cs`, ahora es dato, vía `IMemoryAdapter.FindPlayerSprite(oamEntries)`.

**`--mock-data`**: arranca sin cargar ninguna ROM/emulador, usando `RomLoader.MockMemoryAdapter`
(posición sintética que deambula en un cuadradito, para poder ver el `move` salir de verdad).
Es la prueba de que el motor (red, chat, paneles, jugadores remotos, etiquetas de nombre) no
depende de tener un GBA corriendo — solo de `IMemoryAdapter`. Sin video de fondo (pantalla
negra, no hay framebuffer que mostrar), pero todo lo demás funciona igual:
```
ClientApp.exe --mock-data --rom-id emerald_es
```

El spawn de un personaje nuevo (`littleroot_town` para Esmeralda) tampoco está hardcodeado del
lado servidor: `server/internal/auth/auth.go` lo resuelve contra la tabla
`rom_spawn_points` (rom_id → map_id/pos_x/pos_y). Un rom_id sin fila ahí (como `"mock"`) cae en
un fallback neutro (`map_id="unknown", 0,0`) en vez de romper el registro.

## Fase E — Cliente↔servidor real (✅ hecho y verificado: primer movimiento sincronizado)

`ClientApp/Network/WebSocketClient.cs`: cliente WebSocket real (`System.Net.WebSockets`,
nada de librerías externas) contra `server/internal/ws`, usando **el mismo archivo de
protocolo** que usaría un cliente Godot (`client-stub/Protocol/Messages.cs`, linkeado
directo en el `.csproj` — una sola fuente de verdad, no una copia).

Al arrancar (salvo `--offline`): registra/loguea una cuenta de desarrollo (`client_dev`),
y en el loop principal, si `IMemoryAdapter.GetPlayerPosition()` cambió desde el último envío,
manda `move` al servidor (throttle ~150ms). `map_id` real (con detección de mapa/edificio vía
`GetMapNumber()`) — ver Fase RomLoader más abajo.

**Verificado de punta a punta con dos procesos reales**, no un mock: `scripts/verify-client-sync.js`
se conecta como un segundo jugador y confirma que le llega `player_update` con la posición
*real* leída de la RAM del emulador:
```
player_update recibido: { character_id: '...', map_id: 'littleroot_town', x: 11, y: 11, ... }
OK: la posición real del cliente (leída de la ROM) llegó sincronizada a otro jugador conectado.
```
`x=11, y=11` es exactamente la posición del save state usado — confirma la cadena completa:
RAM del emulador (D.1) → dirección validada (D.3) → `IMemoryAdapter` (D.4) → WebSocket (E) →
servidor → broadcast → otro cliente conectado.

## D.2b — Ver a otros jugadores en pantalla (✅ hecho y verificado: primer overlay real)

`Renderer.SetRemoteMarkers(...)`: segunda pasada de dibujo (shader de vértices con color
plano, sin textura) sobre el framebuffer del emulador — un cuadrado de 12x12px por cada
jugador remoto en el mismo mapa, ubicado según su offset en tiles respecto al jugador local
(la cámara del emulador siempre sigue centrado al jugador local, así que ese offset alcanza;
simplificación conocida: no contempla el clamp de cámara cerca de los bordes del mapa).

`Program.cs` mantiene un diccionario de jugadores remotos actualizado por `player_update`/
`player_joined_map`/`map_players_snapshot`, y lo recalcula contra la posición local real
(`IMemoryAdapter`) cada frame.

**Actualización (Fase C continuación, 2026-07-15)**: las dos limitaciones de protocolo que
esta sección documentaba como pendientes ya se resolvieron del lado del servidor y están
conectadas en `ClientApp/Program.cs`:
- `map_players_snapshot`: se manda una vez, justo tras `login_ok`, con quién ya está en el
  mapa de spawn — verificado con `scripts/map-presence-smoke.js` (un jugador que se conecta
  después ve correctamente a uno que ya estaba, sin esperar a que se mueva).
- `player_left_map` ahora también se dispara al **desconectarse** (no solo al cambiar de
  mapa) — mismo smoke test, verifica que un tercero conectado ve desaparecer al que se fue.

**Verificado con captura real** (`scripts/fake-friend.js` simula un segundo jugador
posicionado 3 tiles a la derecha y 2 abajo del jugador local conocido): el cuadrado rojo
aparece exactamente en esa posición relativa en el framebuffer capturado — confirma que
Fase E (datos de otros jugadores) y D.2b (dibujarlos) funcionan juntos de punta a punta.

## Próximos pasos (pendientes, no implementados todavía)

- Sprites reales en vez de cuadrados de color (necesita arte, o extraer sprites de la ROM).
- Validar `map_bank`/`map_number` (falta prueba cruzando de mapa) y `save_block` (equipo,
  dinero, PC) con la misma metodología de D.3, y sumarlos a `IMemoryAdapter` — el primer
  intento (2026-07-15) no encontró el patrón por diff de memoria; hace falta desreferenciar
  `gSaveBlock1Ptr` en vez de asumir una dirección fija (ver nota en `memory-maps/emerald_es.json`).
- Sacar el `map_id` hardcodeado una vez validado `map_bank`/`map_number`.
- Arrancar el cliente directo en el mundo (saltar el flujo de un jugador de la ROM), con
  login/selección de ROM propios — ver la nota de decisión de producto más arriba.

## ROM(s) de referencia

| ROM | Game code | MD5 | Estado |
|---|---|---|---|
| Pokémon Esmeralda (España) | `BPES` | `67B770405F7B87589E0342513B25FE9B` | En el repo; usada para esta PoC |
| Pokémon Emerald (US) | `BPEE` | — | Falta conseguir; mejor documentada por la comunidad (proyecto de decompilation `pokeemerald`) |
