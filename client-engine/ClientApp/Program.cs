using System.Linq;
using System.Net.Http.Json;
using System.Text.Json;
using ClientApp;
using ClientApp.Network;
using ClientApp.Screens;
using ClientApp.Social;
using LibretroCore;
using PokemonOnline.Protocol;
using RomLoader;

// D.2: la ROM emulada corriendo de fondo, en una ventana Win32 real, renderizada con
// Direct3D 11 (sin embeber la ventana nativa de mGBA ni depender de una lib de alto nivel).
//
// Fase F/G: el motor arranca en SU PROPIA pantalla de inicio de sesión / creación de cuenta /
// selección de ROM (ClientApp.Screens.LoginFlow) — no en la intro de la ROM. Solo después de
// autenticarse y elegir una ROM entra al juego, ya conectado y sincronizado con el servidor.

const int Scale = 4; // GBA: 240x160 nativo. x4 = 960x640, entero, sin distorsión con sampling point.
const int WindowWidth = 240 * Scale;
const int WindowHeight = 160 * Scale;

bool offline = args.Contains("--offline");
// --mock-data: arranca SIN cargar ninguna ROM/emulador — usa RomLoader.MockMemoryAdapter en
// vez de leer memoria real. Es la prueba de la Fase RomLoader-5: si el motor (red, chat,
// paneles, jugadores remotos) funciona igual con esto que con una ROM real, el motor
// realmente no depende de la ROM, solo de la interfaz IMemoryAdapter. Implica dev-boot (no
// tiene sentido mostrar la pantalla de selección de ROM si no vamos a cargar ninguna).
bool mockData = args.Contains("--mock-data");
// --dev-boot / --offline: arranque directo tipo "harness" de desarrollo (F1 EWRAM dumps, D.3,
// --load-state, etc.), saltando la pantalla de login/registro/selección de ROM. offline
// implica dev-boot: no tiene sentido mostrar una pantalla de login para un servidor al que no
// nos vamos a conectar.
bool devBoot = offline || mockData || args.Contains("--dev-boot");
string serverHttp = GetOpt(args, "--server-http") ?? "http://localhost:8080";
string serverWs = GetOpt(args, "--server-ws") ?? "ws://localhost:8080/ws";

string repoRoot = Path.GetFullPath(Path.Combine(AppContext.BaseDirectory, "..", "..", "..", "..", ".."));
string memoryMapsDir = GetOpt(args, "--memory-maps-dir") ?? Path.Combine(repoRoot, "memory-maps");

// Sprites de batalla (Fase battle-3): en vez de descomprimir LZ77 y ubicar tablas de punteros
// en el binario compilado, se reusan los PNG ya extraídos del checkout local de
// pokeemerald-master (mismo asset exacto del juego) — decisión tomada con el usuario, ver
// ClientApp.Battle.BattleSpriteAssets.
string spritesDir = GetOpt(args, "--sprites-dir")
    ?? Path.Combine(repoRoot, "Pokemon Esmeralda", "pokeemerald-master", "pokeemerald-master", "graphics", "pokemon");

// --dump-sprite <species> <front|back> <out.png>: decodifica un sprite y lo vuelca a un PNG
// real (no BMP del backbuffer) SIN crear ventana/device D3D — verificación offline de
// SpritePngLoader (orden de canales, transparencia) más barata que probarlo en vivo.
int dumpSpriteIndex = Array.IndexOf(args, "--dump-sprite");
if (dumpSpriteIndex >= 0 && dumpSpriteIndex + 3 < args.Length)
{
    int species = int.Parse(args[dumpSpriteIndex + 1]);
    string side = args[dumpSpriteIndex + 2];
    string outPath = args[dumpSpriteIndex + 3];
    var loaded = side == "back"
        ? ClientApp.Battle.BattleSpriteAssets.LoadBack(spritesDir, species)
        : ClientApp.Battle.BattleSpriteAssets.LoadFront(spritesDir, species);
    if (loaded == null)
    {
        Console.Error.WriteLine($"No se pudo cargar el sprite de species={species} ({side}) desde {spritesDir}");
        return 1;
    }
    var (rgba, w, h) = loaded.Value;
    using var bmp = new System.Drawing.Bitmap(w, h, System.Drawing.Imaging.PixelFormat.Format32bppArgb);
    var rect = new System.Drawing.Rectangle(0, 0, w, h);
    var data = bmp.LockBits(rect, System.Drawing.Imaging.ImageLockMode.WriteOnly, System.Drawing.Imaging.PixelFormat.Format32bppArgb);
    unsafe
    {
        byte* dst = (byte*)data.Scan0;
        for (int y = 0; y < h; y++)
        {
            byte* row = dst + y * data.Stride;
            for (int x = 0; x < w; x++)
            {
                int i = (y * w + x) * 4;
                row[x * 4 + 0] = rgba[i + 2]; // B
                row[x * 4 + 1] = rgba[i + 1]; // G
                row[x * 4 + 2] = rgba[i + 0]; // R
                row[x * 4 + 3] = rgba[i + 3]; // A
            }
        }
    }
    bmp.UnlockBits(data);
    bmp.Save(outPath, System.Drawing.Imaging.ImageFormat.Png);
    Console.WriteLine($"Sprite volcado: {outPath} ({w}x{h})");
    return 0;
}

// Fase RomLoader-3: ninguna ROM/rom_id/memory-map particular queda hardcodeada en el motor —
// el catálogo (RomCatalog.Discover, ya agnóstico) es la única fuente de "qué ROM se juega hoy
// acá". Se arma una sola vez y lo usan tanto --dev-boot como el flujo normal de LoginFlow más
// abajo. La primera entrada del catálogo es el default cuando no se pasa --rom/--memory-map
// explícito — "la primera ROM que el catálogo encontró instalada", no una ROM en particular.
var catalog = RomCatalog.Discover(memoryMapsDir, repoRoot);
RomCatalogEntry? defaultRom = catalog.Count > 0 ? catalog[0] : null;

// --dump-frame <path> <N>: corre N frames y vuelca el backbuffer a un BMP, para verificar el
// pipeline de render en entornos sin captura de pantalla disponible (ej. RDP sin GPU). En
// modo dev-boot vuelca un frame del JUEGO; en modo normal (con UI de login) vuelca un frame
// de la pantalla de inicio en la que esté parado el usuario en ese momento.
int dumpIndex = Array.IndexOf(args, "--dump-frame");
string? dumpPath = dumpIndex >= 0 && dumpIndex + 1 < args.Length ? args[dumpIndex + 1] : null;
int dumpAfterFrames = dumpIndex >= 0 && dumpIndex + 2 < args.Length && int.TryParse(args[dumpIndex + 2], out int n) ? n : 120;

using var window = new Win32Window("Pokémon Online", WindowWidth, WindowHeight);
using var renderer = new Renderer(window.Handle, WindowWidth, WindowHeight);

// D.2b: jugadores remotos vistos por player_update, para dibujarlos como marcadores y (Fase G)
// para poder targetear por nombre en el SocialPanel (agregar amigo, invitar a grupo, pedir
// trade) sin que el jugador tenga que escribir un UUID a mano.
// ConcurrentDictionary porque se escribe desde el hilo de recepción del WebSocket y se
// lee desde el loop principal (render). No hay limpieza ante desconexión ajena todavía
// (el servidor no manda un evento explícito para eso) — limitación conocida.
var remotePlayers = new System.Collections.Concurrent.ConcurrentDictionary<string, (string Nickname, string MapId, int X, int Y, string Color)>();

// Fase F: chat de texto. chatLog se escribe desde el hilo de recepción del WebSocket (OnMessage)
// y se lee desde el loop principal (render) -> protegido con lock, no ConcurrentQueue, porque
// además hay que recortarlo a un máximo de líneas.
const int MaxChatLines = 8;
var chatLog = new List<(string nickname, string message)>();
var chatLogLock = new object();
var chatInput = new System.Text.StringBuilder();

void AppendChatLog(string nickname, string message)
{
    lock (chatLogLock)
    {
        chatLog.Add((nickname, message));
        while (chatLog.Count > MaxChatLines) chatLog.RemoveAt(0);
    }
}

MgbaCore? core = null; // null en --mock-data: el motor no depende de tener un emulador corriendo
IMemoryAdapter? memAdapter = null;
WebSocketClient? ws = null;
string myCharacterId = "";
string currentMapId = "";
string myColor = "default";
SocialPanel? socialPanel = null;
ClientApp.Battle.BattleScreen? battleScreen = null;
VoiceChat? voice = null;

// Engancha el handler de gameplay y arma el panel social/voz apenas hay sesión autenticada
// — ANTES de cargar la ROM (el paso más lento del arranque). Antes esto pasaba después de
// cargar la ROM: el servidor manda map_players_snapshot (y podría mandar cualquier otra
// notificación) justo después del login_ok, pero WebSocketClient.OnMessage no tenía todavía
// ningún suscriptor durante ese hueco — los mensajes se perdían en silencio (no hay cola, un
// evento sin suscriptores simplemente no hace nada). Verificado en vivo: el jugador que ya
// estaba conectado no aparecía en el mapa hasta que se movía, porque el snapshot inicial se
// perdía siempre que la carga de la ROM tomaba más de un instante.
void WireUpSession()
{
    if (ws == null) return;
    socialPanel = new SocialPanel(ws, myCharacterId, () => remotePlayers, () => myColor);
    battleScreen = new ClientApp.Battle.BattleScreen(ws, myCharacterId, () => memAdapter?.GetParty(), spritesDir);
    ws.OnMessage += GameplayMessageHandler;
    voice = new VoiceChat(ws);
    Console.WriteLine(voice.CaptureAvailable
        ? "[voz] micrófono detectado: mantené V para hablar."
        : "[voz] no se detectó micrófono en este equipo: captura deshabilitada (la reproducción de voz remota sigue intentándose).");
}

if (devBoot)
{
    string romId;

    if (mockData)
    {
        // Fase RomLoader-5: cero MgbaCore, cero ROM, cero memory-map — memAdapter es el mock.
        // "mock" como rom_id a propósito: no hay ninguna fila en rom_spawn_points para eso,
        // así que el servidor cae en su fallback neutro (ver auth.spawnPointFor) — probar el
        // mock del cliente de paso ejercita el fallback del servidor, con la misma corrida.
        memAdapter = new MockMemoryAdapter();
        romId = GetOpt(args, "--rom-id") ?? "mock";
        Console.WriteLine("[mock] --mock-data: sin ROM/emulador, usando RomLoader.MockMemoryAdapter (posición simulada).");
    }
    else
    {
        string? romPath = GetOpt(args, "--rom") ?? defaultRom?.RomPath;
        if (romPath == null || !File.Exists(romPath))
        {
            Console.Error.WriteLine($"No se encontró ninguna ROM jugable ({(romPath == null ? "el catálogo está vacío" : romPath)}).");
            Console.Error.WriteLine("Pasala con --rom <path>, o poné un .gba junto a su memory-map en memory-maps/ para que el catálogo la encuentre sola.");
            return 1;
        }

        var realCore = new MgbaCore();
        core = realCore;
        if (!realCore.LoadGame(romPath))
        {
            Console.Error.WriteLine("retro_load_game devolvió false: el core rechazó la ROM.");
            return 1;
        }
        if (realCore.Format != PixelFormat.RGB565)
        {
            Console.Error.WriteLine($"Formato de píxel inesperado: {realCore.Format} (el conversor de Renderer asume RGB565).");
            return 1;
        }

        // --load-sav <path>: carga un .sav real (SRAM de guardado en batería, formato estándar
        // de cualquier emulador/flashcart) en vez de un savestate del emulador — permite arrancar
        // directo en una partida ya avanzada (Pokédex recibida, equipo con varios Pokémon, etc.)
        // sin rejugar la intro. A diferencia de --load-state, no depende de la build del core.
        string? loadSavPath = GetOpt(args, "--load-sav");
        if (loadSavPath != null)
        {
            if (!File.Exists(loadSavPath))
            {
                Console.Error.WriteLine($"No existe el .sav: {loadSavPath}");
                return 1;
            }
            realCore.LoadSaveRam(File.ReadAllBytes(loadSavPath));
            Console.WriteLine($".sav cargado desde {loadSavPath}");
        }

        // --load-state <path>: retoma una partida guardada con F3 en una corrida anterior, para no
        // tener que rejugar la intro cada vez que se reinicia el cliente durante el desarrollo.
        string? loadStatePath = GetOpt(args, "--load-state");
        if (loadStatePath != null)
        {
            if (!File.Exists(loadStatePath))
            {
                Console.Error.WriteLine($"No existe el estado: {loadStatePath}");
                return 1;
            }
            realCore.RunFrame(); // asegura que el core inicializó sus buffers antes de deserializar
            realCore.LoadState(File.ReadAllBytes(loadStatePath));
            Console.WriteLine($"Estado cargado desde {loadStatePath}");
        }

        string? memoryMapPath = GetOpt(args, "--memory-map") ?? defaultRom?.MemoryMapPath;
        romId = GetOpt(args, "--rom-id") ?? defaultRom?.RomId ?? "unknown";
        if (memoryMapPath != null && File.Exists(memoryMapPath))
        {
            memAdapter = new GbaMemoryAdapter(realCore, MemoryMapConfig.LoadFromFile(memoryMapPath));
            Console.WriteLine($"Memory map: {memoryMapPath}");
        }
        else
        {
            Console.WriteLine($"(sin memory-map{(memoryMapPath != null ? $" en {memoryMapPath}" : "")}: no se va a poder leer/enviar posición real)");
        }
    }

    if (!offline)
    {
        try
        {
            using var http = new HttpClient();
            const string username = "client_dev";
            const string password = "clientdev123";

            var registerBody = new { username, email = $"{username}@example.com", password, rom_id = romId, nickname = "ClientDev" };
            // .GetAwaiter().GetResult() en vez de await A PROPÓSITO: una consola sin
            // SynchronizationContext puede reanudar un `await` en OTRO hilo del thread-pool.
            // La ventana Win32 (y su cola de mensajes) es afín al hilo que la creó — si el loop
            // principal terminara corriendo en un hilo distinto, jamás vería el teclado real.
            var registerResponse = http.PostAsJsonAsync($"{serverHttp}/register", registerBody).GetAwaiter().GetResult();
            Console.WriteLine(registerResponse.IsSuccessStatusCode
                ? $"Cuenta '{username}' registrada."
                : $"Registro de '{username}' devolvió {(int)registerResponse.StatusCode} (probablemente ya existía) — se intenta login igual.");

            ws = new WebSocketClient();
            ws.OnError += ex => Console.Error.WriteLine($"[ws] error: {ex.Message}");
            ws.ConnectAsync(serverWs).GetAwaiter().GetResult();

            // Enganchar el buffer ANTES de mandar "login" (no como onOther de LoginBlocking):
            // en un servidor local el hilo de recepción puede procesar login_ok y el
            // map_players_snapshot que le sigue tan pegados que un handler temporal instalado
            // recién dentro de LoginBlocking llega tarde — ver el comentario largo en
            // LoginFlow.TryConnect, mismo motivo, mismo fix. Se reproduce al final, una vez
            // enganchado GameplayMessageHandler, para no perder nada en silencio.
            var earlyMessages = new List<(string Type, JsonElement Payload)>();
            ws.OnMessage += (type, payload) => earlyMessages.Add((type, payload));
            var login = LoginFlow.LoginBlocking(ws, username, password, TimeSpan.FromSeconds(6));
            if (login.Error != null)
            {
                Console.Error.WriteLine($"[ws] login falló: {login.Error}");
                ws = null;
            }
            else
            {
                myCharacterId = login.CharacterId!;
                currentMapId = login.MapId!;
                myColor = login.Color ?? "default";
                Console.WriteLine($"[ws] login_ok character_id={myCharacterId} map={currentMapId} pos=({login.X},{login.Y})");
                WireUpSession();
                foreach (var (type, payload) in earlyMessages) GameplayMessageHandler(type, payload);
            }
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine($"No se pudo conectar al servidor ({serverWs}): {ex.Message}");
            Console.Error.WriteLine("Seguimos en modo offline (el emulador funciona igual, solo no hay sync).");
            ws = null;
        }
    }
}
else
{
    var loginFlow = new LoginFlow(window, renderer, serverHttp, serverWs, catalog) { DumpPath = dumpPath, DumpAfterFrames = dumpAfterFrames };
    GameSession? session = loginFlow.Run();
    if (session == null)
    {
        Console.WriteLine("Cerrado desde la pantalla de inicio.");
        return 0;
    }

    ws = session.Ws;
    myCharacterId = session.CharacterId;
    currentMapId = session.MapId;
    myColor = session.Color;
    Console.WriteLine($"[ws] sesión iniciada: character_id={myCharacterId} map={currentMapId} pos=({session.X},{session.Y}) ROM={session.Rom.DisplayName}");
    WireUpSession(); // antes de cargar la ROM (ver comentario en la declaración) — no después

    // Reproducir, en orden, lo que haya llegado mientras el jugador todavía estaba en la
    // pantalla de selección de ROM (ver GameSession.BufferedMessages / LoginFlow) — sin esto,
    // un map_players_snapshot que llegara justo en ese momento se perdía en silencio.
    foreach (var (type, payload) in session.BufferedMessages)
        GameplayMessageHandler(type, payload);

    core = new MgbaCore();
    if (!core.LoadGame(session.Rom.RomPath))
    {
        Console.Error.WriteLine("retro_load_game devolvió false: el core rechazó la ROM.");
        return 1;
    }
    if (core.Format != PixelFormat.RGB565)
    {
        Console.Error.WriteLine($"Formato de píxel inesperado: {core.Format} (el conversor de Renderer asume RGB565).");
        return 1;
    }

    memAdapter = new GbaMemoryAdapter(core, MemoryMapConfig.LoadFromFile(session.Rom.MemoryMapPath));
    dumpPath = null; // ya se consumió (o no) dentro de LoginFlow; no volcar de nuevo en el juego
}

if (core != null) core.FrameReady += (data, width, height, pitch) => renderer.UpdateFrame(data, width, height, pitch);

// Fase G: extracción de sprites directo de OAM/VRAM/paleta del GBA (hardware estándar, no
// direcciones específicas de esta ROM — ver LibretroCore.SpriteExtractor). Se reintenta cada
// SpriteRecaptureInterval frames en vez de una sola vez: la primera captura puede caer en un
// frame de transición (menú, diálogo) donde el jugador no está en su pose normal; reintentar
// barato (es una lectura de memoria + una textura chica) autocorrige sin que el jugador tenga
// que hacer nada. Nulos en --mock-data: no hay VRAM/OAM real que leer sin un core corriendo.
const int SpriteRecaptureInterval = 30;
GbaMemoryBus? gbaMemBus = core != null ? new GbaMemoryBus(core) : null;
SpriteExtractor? spriteExtractor = gbaMemBus != null ? new SpriteExtractor(gbaMemBus) : null;

void TryRecapturePlayerSprite()
{
    // Fase RomLoader-2: qué OBJ de OAM es "el jugador" (tamaño/paleta/posición esperada) ya no
    // se decide acá — es una convención de ESTA ROM (memory-maps/*.json player.sprite), no algo
    // que el motor deba saber. Si el memory-map no la define, simplemente no hay recaptura de
    // sprite remoto (el motor sigue andando, solo sin ese overlay).
    if (memAdapter == null || spriteExtractor == null) return;
    try
    {
        OamEntry? candidate = memAdapter.FindPlayerSprite(spriteExtractor.ReadOam());
        if (candidate is not { } sprite) return; // nada visible todavía (ej. pantalla de carga)

        byte[] rgba = spriteExtractor.DecodeSprite(sprite.TileIndex, sprite.WidthPx, sprite.HeightPx, sprite.PaletteIndex, sprite.HFlip, sprite.VFlip);
        renderer.UploadSpriteTexture(rgba, sprite.WidthPx, sprite.HeightPx);
    }
    catch (ArgumentOutOfRangeException)
    {
        // El core todavía no anunció las regiones de memoria (primeros frames tras cargar la
        // ROM) o el juego está en un estado sin OBJs válidos — se reintenta solo en el próximo
        // intervalo, no hace falta romper el frame por esto.
    }
}

void GameplayMessageHandler(string type, JsonElement payload)
{
    switch (type)
    {
        case "player_update":
        case "player_joined_map":
            var upd = payload.Deserialize<PlayerUpdatePayload>();
            if (upd != null && upd.CharacterId != myCharacterId)
            {
                remotePlayers[upd.CharacterId] = (upd.Nickname, upd.MapId, upd.X, upd.Y, upd.Color);
                Console.WriteLine($"[ws] {type} {upd.CharacterId} -> ({upd.X},{upd.Y}) mapa={upd.MapId}");
            }
            else if (upd != null && upd.CharacterId == myCharacterId)
            {
                // Confirmación del propio color tras un "set_color" (ver Router.handleSetColor,
                // que manda el player_update también al emisor) — recién ahí se sabe que quedó
                // guardado, no antes (optimista sería más simple, pero mentiría si el servidor
                // lo rechazara por algún motivo futuro).
                myColor = upd.Color;
            }
            break;
        case "player_left_map":
            var left = payload.Deserialize<PlayerUpdatePayload>();
            if (left != null) remotePlayers.TryRemove(left.CharacterId, out _);
            Console.WriteLine($"[ws] player_left_map {left?.CharacterId}");
            break;
        case "map_players_snapshot":
            var snapshot = payload.Deserialize<MapPlayersSnapshotPayload>();
            if (snapshot != null)
            {
                foreach (var p in snapshot.Players)
                {
                    if (p.CharacterId != myCharacterId)
                        remotePlayers[p.CharacterId] = (p.Nickname, p.MapId, p.X, p.Y, p.Color);
                }
                Console.WriteLine($"[ws] map_players_snapshot: {snapshot.Players.Length} jugador(es) ya en el mapa");
            }
            break;
        case "move_rejected":
            var rejected = payload.Deserialize<MoveRejectedPayload>();
            Console.WriteLine($"[ws] move_rejected -> corregido a ({rejected?.X},{rejected?.Y})");
            break;
        case "chat_message":
            var chat = payload.Deserialize<ChatMessagePayload>();
            if (chat != null)
            {
                AppendChatLog(chat.FromNickname, chat.Message);
                Console.WriteLine($"[chat] {chat.FromNickname}: {chat.Message}");
            }
            break;
        case "error":
            Console.Error.WriteLine($"[ws] error del servidor: {payload}");
            break;
        default:
            Console.WriteLine($"[ws] {type}: {payload}");
            break;
    }
    // Fase G: amigos/grupo/comercio — el panel ignora los tipos que no le interesan.
    socialPanel?.HandleMessage(type, payload);
    // Fase battle-4: batalla PvP — mismo criterio, ignora lo que no le interesa.
    battleScreen?.HandleMessage(type, payload);
}


// Mapeo de teclado -> joypad de GBA (convención clásica de emuladores tipo VBA):
// flechas = D-Pad, Z = A, X = B, Enter = Start, Backspace = Select, A/S = L/R.
// Tab = turbo (mantenido): corre varios frames de emulación por cada frame real,
// para acelerar diálogos/desplazamiento sin tener que subir la "TEXT SPEED" del juego.
const int VK_UP = 0x26, VK_DOWN = 0x28, VK_LEFT = 0x25, VK_RIGHT = 0x27;
const int VK_RETURN = 0x0D, VK_BACK = 0x08, VK_TAB = 0x09, VK_ESCAPE = 0x1B;
const int VK_Z = 0x5A, VK_X = 0x58, VK_A = 0x41, VK_S = 0x53, VK_V = 0x56;
// F2 (no una tecla de letra) para abrir el chat: las teclas de letra pueden generar un
// WM_CHAR vía TranslateMessage además del WM_KEYDOWN — una tecla de función nunca produce
// carácter, así que no hay riesgo de que la propia tecla de apertura se cuele en el mensaje.
const int VK_CHAT_TOGGLE = 0x71; // F2
const int TurboMultiplier = 6;

// Fase F: mientras el chat de texto está activo, el juego no debe recibir input (si no,
// escribir "arriba" movería al personaje además de escribirse en el mensaje).
bool chatActive = false;

if (core != null) core.InputState = (port, device, index, id) =>
{
    if (chatActive || (socialPanel != null && socialPanel.IsActive) || (battleScreen != null && battleScreen.IsActive)) return 0;
    if (port != 0 || device != RetroDevice.Joypad) return 0;
    bool pressed = (JoypadButton)id switch
    {
        JoypadButton.Up => window.IsKeyDown(VK_UP),
        JoypadButton.Down => window.IsKeyDown(VK_DOWN),
        JoypadButton.Left => window.IsKeyDown(VK_LEFT),
        JoypadButton.Right => window.IsKeyDown(VK_RIGHT),
        JoypadButton.A => window.IsKeyDown(VK_Z),
        JoypadButton.B => window.IsKeyDown(VK_X),
        JoypadButton.Start => window.IsKeyDown(VK_RETURN),
        JoypadButton.Select => window.IsKeyDown(VK_BACK),
        JoypadButton.L => window.IsKeyDown(VK_A),
        JoypadButton.R => window.IsKeyDown(VK_S),
        _ => false,
    };
    return pressed ? (short)1 : (short)0;
};

// F1 = volcar EWRAM (256KB crudos) + una captura, con timestamp, mientras se juega en vivo.
// Herramienta de desarrollo para encontrar/validar direcciones de memoria (ver D.3 en el README).
const int VK_F1 = 0x70, VK_F3 = 0x72, VK_F6 = 0x75, VK_F8 = 0x77;
string dumpDir = Path.Combine(AppContext.BaseDirectory, "dumps");
Directory.CreateDirectory(dumpDir);
bool prevF1 = false, prevF3 = false, prevF6 = false, prevF8 = false, prevChatToggle = false;

(string MapId, int X, int Y)? lastSentPos = null;
DateTime lastMoveSentAt = DateTime.MinValue;
var moveSendInterval = TimeSpan.FromMilliseconds(150); // throttle: máx ~6-7 "move" por segundo
byte? spawnMapNumber = null;
byte? currentMapNumber = null;

// --debug-battle <mySpecies> <oppSpecies>: inyecta un "battle_start" sintético (sin desafiar a
// nadie de verdad) directo en BattleScreen — verificación visual barata del layout (sprites/HP
// bars/menú) sin necesitar dos clientes reales completando el desafío/aceptación por WS. Se
// dispara una sola vez, un par de frames después de que arranca el loop (para que WireUpSession
// ya haya corrido y battleScreen exista).
int debugBattleIndex = Array.IndexOf(args, "--debug-battle");
(int Mine, int Opponent)? debugBattleSpecies = debugBattleIndex >= 0 && debugBattleIndex + 2 < args.Length
    ? (int.Parse(args[debugBattleIndex + 1]), int.Parse(args[debugBattleIndex + 2]))
    : null;

Console.WriteLine("Ventana abierta. Cerrala para salir.");
Console.WriteLine($"F1 = volcar EWRAM + captura | F3 = guardar estado -> {dumpDir}");
int frame = 0;
while (!window.ShouldClose)
{
    window.PumpMessages();
    if (window.ShouldClose) break;

    if (core != null)
    {
        int steps = window.IsKeyDown(VK_TAB) ? TurboMultiplier : 1;
        for (int i = 0; i < steps; i++)
            core.RunFrame(); // dispara FrameReady sincrónicamente -> ya actualiza la textura
    }
    frame++;

    if (debugBattleSpecies != null && frame == 3 && battleScreen != null)
    {
        var fakeStart = new BattleStartPayload
        {
            BattleSessionId = "debug",
            Yours = new BattlePokemonPayload { PokemonId = "debug-mine", SpeciesId = debugBattleSpecies.Value.Mine, Nickname = "MITUYO", Level = 5, CurrentHp = 14, MaxHp = 20 },
            Opponent = new BattlePokemonPayload { PokemonId = "debug-opp", SpeciesId = debugBattleSpecies.Value.Opponent, Nickname = "RIVAL", Level = 5, CurrentHp = 18, MaxHp = 22 },
        };
        var fakeElement = JsonSerializer.SerializeToElement(fakeStart);
        battleScreen.HandleMessage("battle_start", fakeElement);
        battleScreen.HandleMessage("battle_turn_result", JsonSerializer.SerializeToElement(new BattleTurnResultPayload
        {
            BattleSessionId = "debug",
            Events = [new BattleEventPayload { Type = "damage", ActorCharacterId = myCharacterId, MoveId = 10, Damage = 4, Effectiveness = 1.0 }],
            YourHp = 14,
            OpponentHp = 18,
        }));
        debugBattleSpecies = null;
    }

    PlayerPosition? localPos = null;
    if (memAdapter != null)
    {
        try { localPos = memAdapter.GetPlayerPosition(); }
        catch (Exception ex) { Console.Error.WriteLine($"[memoria] {ex.Message}"); }
    }

    // D.3 (validado 2026-07-16): currentMapId es el nombre del mapa de SPAWN (fijo, viene del
    // login) — nunca hubo forma de detectar que el jugador entró a un edificio distinto dentro
    // del mismo pueblo hasta ahora. map_number_address da un identificador numérico crudo, sin
    // nombre legible todavía, pero estable y distinto por edificio: alcanza para comparar por
    // IGUALDAD si dos jugadores están realmente en el mismo mapa (spawnMapNumber = "estás en tu
    // mapa de origen", cualquier otro valor = un mapa DISTINTO, sufijado al nombre para no
    // colisionar con el mismo string usado por otros clientes en el mapa de spawn real).
    if (memAdapter != null)
    {
        try
        {
            byte mapNumber = memAdapter.GetMapNumber();
            spawnMapNumber ??= mapNumber;
            currentMapNumber = mapNumber;
        }
        catch (Exception) { /* memory-map sin map_number_address para esta ROM, o EWRAM no lista aún: se reintenta el próximo frame */ }
    }
    string effectiveMapId = (currentMapNumber is null || currentMapNumber == spawnMapNumber)
        ? currentMapId
        : $"{currentMapId}#{currentMapNumber}";

    // Fase G: sprites reales de jugadores remotos, no cuadrados de color (D.2b). Se reutiliza
    // el sprite del jugador LOCAL (el único que el propio emulador realmente dibuja) para
    // representar a los demás — no hay forma de que el emulador renderice un personaje que no
    // sea el suyo, así que "un sprite real gen érico" ya es la mejora honesta posible sin
    // simular nuestro propio mundo (ver la conversación sobre el enfoque tipo PokeMMO).
    if (frame % SpriteRecaptureInterval == 0) TryRecapturePlayerSprite();

    if (localPos != null)
    {
        var offsets = new List<(float, float, float, float, float)>();
        foreach (var (charId, remote) in remotePlayers)
        {
            if (remote.MapId != effectiveMapId) continue;
            var (tr, tg, tb) = SpriteColors.Rgb(remote.Color);
            offsets.Add((remote.X - localPos.Value.X, remote.Y - localPos.Value.Y, tr, tg, tb));
        }
        renderer.SetRemoteSprites(offsets);
    }

    bool battleActive = battleScreen != null && battleScreen.IsActive;
    // BattleScreen toma control total de pantalla/input: si el panel social seguía abierto
    // (ej. el propio retador, que nunca pasó por el "Enter: aceptar" que cierra el panel del
    // otro lado, ver SocialPanel.Tab.Battle), se fuerza su cierre para que no queden dos
    // superficies dibujándose/compitiendo por el teclado a la vez.
    if (battleActive) socialPanel?.Close();
    bool socialPanelActive = !battleActive && socialPanel != null && socialPanel.IsActive;

    // Fase F: abrir/cerrar el chat con F2 (como Minecraft/PokeMMO usan T, pero una tecla de
    // letra puede colarse como WM_CHAR en el propio mensaje — F2 nunca genera carácter).
    // Edge-detected: un toque = un toggle, no mantenido. No se abre si el panel social (F5)
    // ya está abierto, ni durante una batalla, para no tener dos superficies de input
    // compitiendo por el teclado.
    bool chatToggleNow = window.IsKeyDown(VK_CHAT_TOGGLE);
    if (chatToggleNow && !prevChatToggle && !chatActive && !socialPanelActive && !battleActive && ws != null)
    {
        chatActive = true;
        chatInput.Clear();
        window.ConsumeTypedChars(); // por las dudas, descartar cualquier tipeo que haya quedado en cola
    }
    prevChatToggle = chatToggleNow;

    var typedChars = window.ConsumeTypedChars();

    if (battleActive)
    {
        battleScreen!.HandleInput(window);
    }
    else if (chatActive)
    {
        foreach (char c in typedChars)
        {
            if (c == '\r')
            {
                string msg = chatInput.ToString().Trim();
                if (msg.Length > 0 && ws != null)
                {
                    // No hace falta eco local: el canal "local" del servidor te incluye a vos
                    // mismo en el broadcast (confirmado en Fase A) — chat_message va a volver.
                    // .GetAwaiter().GetResult() en vez de await (ver nota de threading más
                    // arriba): un envío ocasional disparado por Enter puede bloquear un
                    // instante sin problema, pero un `await` acá sí podría sacar al loop
                    // principal del hilo dueño de la ventana.
                    ws.SendAsync("send_chat", new SendChatPayload { Channel = "local", Message = msg }).GetAwaiter().GetResult();
                }
                chatActive = false;
                chatInput.Clear();
            }
            else if (c == '\b')
            {
                if (chatInput.Length > 0) chatInput.Remove(chatInput.Length - 1, 1);
            }
            else if (c == 0x1B) // Escape también puede llegar como WM_CHAR en algunos layouts
            {
                chatActive = false;
                chatInput.Clear();
            }
            else if (!char.IsControl(c))
            {
                if (chatInput.Length < 200) chatInput.Append(c);
            }
        }
        // Fallback: Escape detectado por VK (no todos los layouts mandan WM_CHAR para Escape).
        if (window.IsKeyDown(VK_ESCAPE)) { chatActive = false; chatInput.Clear(); }
    }
    else
    {
        // Fase G: panel social (F5) — solo recibe input si el chat no está activo, misma
        // exclusión mutua que arriba. El propio SocialPanel hace su detección de flanco de F5.
        socialPanel?.HandleInput(window, typedChars);
    }

    // Fase F: voz por push-to-talk. Solo cuando ninguna superficie de texto está activa (si
    // no, la V se colaría en el mensaje) y solo si hay micrófono real detectado en este equipo.
    voice?.SetTalking(!chatActive && !socialPanelActive && !battleActive && window.IsKeyDown(VK_V));

    renderer.ClearText();
    DrawChatUi();
    DrawRemoteNameTags();
    socialPanel?.Draw(renderer, WindowWidth, WindowHeight);
    battleScreen?.Draw(renderer, WindowWidth, WindowHeight); // encima de todo: reemplaza la vista mientras dura la batalla
    renderer.Render(); // un solo Present por iteración: el turbo se siente como "avance rápido", no como parpadeo

    // Etiqueta con el nickname sobre cada sprite remoto, para diferenciarlos a simple vista
    // (junto con el color, ver SpriteColors). Misma matemática de anclaje que
    // Renderer.SetRemoteSprites (pie del sprite en bottomPxY, 16x32 fijo — mismo supuesto que
    // ya usa TryRecapturePlayerSprite para identificar el OBJ del jugador), pero convertida a
    // píxeles de VENTANA (AddText vive en ese espacio, no en el de 240x160 del GBA — ver
    // comentario de Renderer.AddText) multiplicando por el factor de escala real de la ventana.
    void DrawRemoteNameTags()
    {
        if (localPos == null) return;
        const float GbaWidthPx = 240f, GbaHeightPx = 160f, TilePx = 16f, SpriteHeightPx = 32f, LabelGapPx = 4f;
        float scaleX = WindowWidth / GbaWidthPx, scaleY = WindowHeight / GbaHeightPx;

        foreach (var (charId, remote) in remotePlayers)
        {
            if (remote.MapId != effectiveMapId || remote.Nickname.Length == 0) continue;
            float dx = remote.X - localPos.Value.X, dy = remote.Y - localPos.Value.Y;
            float centerPxX = GbaWidthPx / 2f + dx * TilePx;
            float topPxY = GbaHeightPx / 2f + dy * TilePx + TilePx / 2f - SpriteHeightPx;

            float screenX = centerPxX * scaleX;
            float screenY = (topPxY * scaleY) - renderer.TextLineHeight - LabelGapPx;
            float textWidth = renderer.MeasureTextWidth(remote.Nickname, scale: 0.8f);
            renderer.AddText(remote.Nickname, screenX - textWidth / 2f, screenY, 1f, 1f, 1f, 0.9f, scale: 0.8f);
        }
    }

    void DrawChatUi()
    {
        const float PanelX = 8f, PanelBottomMargin = 20f;
        List<(string nickname, string message)> logSnapshot;
        lock (chatLogLock) logSnapshot = new List<(string, string)>(chatLog);

        float lineH = renderer.TextLineHeight;
        float y = WindowHeight - PanelBottomMargin - lineH * (logSnapshot.Count + (chatActive ? 1 : 0));
        foreach (var (nickname, message) in logSnapshot)
        {
            renderer.AddText($"{nickname}: {message}", PanelX, y, 1f, 1f, 1f, 0.95f);
            y += lineH;
        }
        if (chatActive)
        {
            renderer.AddText($"> {chatInput}_", PanelX, y, 0.4f, 1f, 0.4f, 1f);
        }
        else if (ws != null && !socialPanelActive)
        {
            string hint = "F2 = chat   F5 = amigos/grupo/trade";
            if (voice != null && voice.CaptureAvailable) hint += "   V = hablar";
            renderer.AddText(hint, PanelX, WindowHeight - PanelBottomMargin - lineH, 0.7f, 0.7f, 0.7f, 0.6f);
        }
    }

    // Fase E: si la posición real cambió, avisarle al servidor (throttleado).
    if (ws != null && localPos != null
        && DateTime.UtcNow - lastMoveSentAt >= moveSendInterval
        && (lastSentPos is null || lastSentPos.Value.MapId != effectiveMapId
            || lastSentPos.Value.X != localPos.Value.X || lastSentPos.Value.Y != localPos.Value.Y))
    {
        // Fire-and-forget (sin await): esto corre hasta ~6-7 veces/segundo mientras el
        // jugador camina — un await acá sacaría al loop principal del hilo dueño de la
        // ventana en cuanto el envío no completara sincrónicamente (ver nota de threading
        // más arriba). Perder un "move" ocasional no es grave: el siguiente lo corrige.
        var moveTask = ws.SendAsync("move", new MovePayload
        {
            MapId = effectiveMapId, X = localPos.Value.X, Y = localPos.Value.Y, Facing = "down", State = "walking",
        });
        moveTask.ContinueWith(t => Console.Error.WriteLine($"[ws] error enviando move: {t.Exception?.GetBaseException().Message}"),
            TaskContinuationOptions.OnlyOnFaulted);
        lastSentPos = (effectiveMapId, localPos.Value.X, localPos.Value.Y);
        lastMoveSentAt = DateTime.UtcNow;
    }

    bool f1Now = window.IsKeyDown(VK_F1);
    if (f1Now && !prevF1)
    {
        string stamp = DateTime.Now.ToString("HHmmss_fff");
        if (core == null)
        {
            Console.WriteLine("[diag] F1 sin efecto: no hay core cargado (--mock-data).");
        }
        else
        {
            if (args.Contains("--dump-regions"))
            {
                Console.WriteLine($"[diag] {core.Regions.Count} regiones de memoria:");
                foreach (var r in core.Regions)
                    Console.WriteLine($"[diag]   name=\"{r.Name}\" start=0x{r.Start:X8} len=0x{r.Len:X} flags=0x{r.Flags:X}");
            }
            if (memAdapter != null)
            {
                var money = memAdapter.GetMoney();
                Console.WriteLine(money != null ? $"[diag] money = {money}" : "[diag] money = (memory-map sin save_block_pointers)");
                var party = memAdapter.GetParty();
                if (party != null)
                {
                    Console.WriteLine($"[diag] equipo ({party.Count}):");
                    foreach (var mon in party)
                        Console.WriteLine($"[diag]   species={mon.Species} personality=0x{mon.Personality:X8} otId=0x{mon.OtId:X8} checksumValid={mon.ChecksumValid}");
                }
                const int FLAG_SYS_CLOCK_SET = 0x860 + 0x35;
                Console.WriteLine($"[diag] FLAG_SYS_CLOCK_SET = {memAdapter.GetFlag(FLAG_SYS_CLOCK_SET)}");
            }
            var ewram = core.FindEwram();
            if (ewram != null)
            {
                unsafe
                {
                    var span = new ReadOnlySpan<byte>((void*)ewram.Value.Ptr, (int)ewram.Value.Len);
                    File.WriteAllBytes(Path.Combine(dumpDir, $"ewram_{stamp}.bin"), span.ToArray());
                }
            }
            if (args.Contains("--dump-iwram"))
            {
                var iwram = core.Regions.FirstOrDefault(r => r.Start == 0x03000000);
                if (iwram.Ptr != IntPtr.Zero)
                {
                    unsafe
                    {
                        var span = new ReadOnlySpan<byte>((void*)iwram.Ptr, (int)iwram.Len);
                        File.WriteAllBytes(Path.Combine(dumpDir, $"iwram_{stamp}.bin"), span.ToArray());
                    }
                }
            }
        }
        renderer.CaptureToFile(Path.Combine(dumpDir, $"shot_{stamp}.bmp"));
        Console.WriteLine($"Volcado: shot_{stamp}.bmp" + (core != null ? $" + ewram_{stamp}.bin" : ""));
    }
    prevF1 = f1Now;

    bool f3Now = window.IsKeyDown(VK_F3);
    if (f3Now && !prevF3)
    {
        if (core == null)
        {
            Console.WriteLine("[diag] F3 sin efecto: no hay core cargado (--mock-data).");
        }
        else
        {
            string path = Path.Combine(dumpDir, "savestate.bin");
            File.WriteAllBytes(path, core.SaveState());
            Console.WriteLine($"Estado guardado: {path}");
        }
    }
    prevF3 = f3Now;

    bool f6Now = window.IsKeyDown(VK_F6);
    if (f6Now && !prevF6 && args.Contains("--dump-sprites") && core != null)
    {
        var bus = new LibretroCore.GbaMemoryBus(core);
        var extractor = new LibretroCore.SpriteExtractor(bus);
        Console.WriteLine($"[sprites] mapeo 1D de OBJ: {extractor.Is1DObjMapping}");
        var oam = extractor.ReadOam();
        var visible = oam.Where(e => e.Visible).OrderBy(e => Math.Abs(e.X - 112) + Math.Abs(e.Y - 68)).ToList();
        Console.WriteLine($"[sprites] {visible.Count} entradas OAM visibles (ordenadas por cercanía al centro ~112,68):");
        foreach (var e in visible.Take(20))
            Console.WriteLine($"[sprites]   idx={e.Index} x={e.X} y={e.Y} {e.WidthPx}x{e.HeightPx} tile={e.TileIndex} pal={e.PaletteIndex} hflip={e.HFlip} vflip={e.VFlip} prio={e.Priority}");

        string stamp = DateTime.Now.ToString("HHmmss_fff");
        foreach (var e in visible.Take(6))
        {
            byte[] rgba = extractor.DecodeSprite(e.TileIndex, e.WidthPx, e.HeightPx, e.PaletteIndex, e.HFlip, e.VFlip);
            string path = Path.Combine(dumpDir, $"sprite_{stamp}_idx{e.Index}_{e.WidthPx}x{e.HeightPx}.bmp");
            WriteRgbaBmp(path, e.WidthPx, e.HeightPx, rgba);
        }
        Console.WriteLine($"[sprites] volcadas hasta 6 candidatas a dumps/sprite_{stamp}_*.bmp");
    }
    prevF6 = f6Now;

    // F8 = prueba manual y temporal de escritura (dinero + inicial), solo con --test-write —
    // verifica en vivo que GbaMemoryAdapter.SetMoney()/SetPartyPokemon() funcionan de punta a
    // punta contra memoria real, no solo en teoría (ver gen3_save_pointers memory).
    bool f8Now = window.IsKeyDown(VK_F8);
    if (f8Now && !prevF8 && args.Contains("--test-write") && memAdapter != null && core != null)
    {
        RomLoader.NewGameBootstrap.Apply(memAdapter, startingMoney: 999999,
            RomLoader.StarterCatalog.SpeciesTreecko, nickname: "TREECKO", otName: "TEST", new Random(42));
        // gPlayerParty (0x020244EC, global estático EWRAM_DATA) es la copia que el juego
        // realmente muestra en pantalla durante el gameplay EN CURSO — NewGameBootstrap.Apply
        // solo escribe SaveBlock1 (la copia persistida), que es lo correcto para el uso real
        // (aplicarlo antes de que el juego copie a gPlayerParty). Esta prueba corre a mitad de
        // partida, así que además hay que pisar gPlayerParty a mano para verlo reflejado ya
        // mismo en pantalla — no hace falta en el flujo real.
        var bus = new LibretroCore.GbaMemoryBus(core);
        var verifySpec = RomLoader.StarterCatalog.BuildStarterSpec(RomLoader.StarterCatalog.SpeciesTreecko, "TREECKO", "TEST", new Random(42));
        bus.WriteBytes(0x020244EC + 5u * 100u, RomLoader.Gen3Codec.BuildFullPartySlot(verifySpec));
        Console.WriteLine("[diag] NewGameBootstrap.Apply() aplicado (money=999999, party[0]=Treecico nv5, flags de intro) + gPlayerParty[5] para verificación visual. Presioná F1 para chequear, o abrí el menú Pokémon.");
    }
    prevF8 = f8Now;

    if (dumpPath != null && frame == dumpAfterFrames)
    {
        renderer.CaptureToFile(dumpPath);
        Console.WriteLine($"Frame {frame} volcado a {dumpPath}");
        break;
    }
}

voice?.Dispose();
if (ws != null)
    ws.DisposeAsync().GetAwaiter().GetResult();
core?.Dispose();

Console.WriteLine("Cerrado.");
return 0;

static string? GetOpt(string[] args, string name)
{
    int idx = Array.IndexOf(args, name);
    return idx >= 0 && idx + 1 < args.Length ? args[idx + 1] : null;
}

// Volcado de diagnóstico (--dump-sprites, F6): RGBA8 top-down a BMP de 32bpp con canal alfa,
// para inspeccionar visualmente los sprites decodificados de OAM/VRAM antes de usarlos en el
// renderer de verdad.
static void WriteRgbaBmp(string path, int width, int height, byte[] rgbaTopDown)
{
    int rowBytes = width * 4;
    int dataSize = rowBytes * height;
    int fileSize = 14 + 40 + dataSize;

    using var fs = new FileStream(path, FileMode.Create, FileAccess.Write);
    using var w = new BinaryWriter(fs);

    w.Write((byte)'B'); w.Write((byte)'M');
    w.Write(fileSize);
    w.Write(0);
    w.Write(14 + 40);

    w.Write(40);
    w.Write(width);
    w.Write(-height); // negativo = top-down
    w.Write((short)1);
    w.Write((short)32);
    w.Write(0);
    w.Write(dataSize);
    w.Write(0); w.Write(0);
    w.Write(0); w.Write(0);

    // BMP espera BGRA en memoria; el RGBA que decodificamos viene en orden RGBA -> reordenar.
    var bgra = new byte[rgbaTopDown.Length];
    for (int i = 0; i < rgbaTopDown.Length; i += 4)
    {
        bgra[i + 0] = rgbaTopDown[i + 2];
        bgra[i + 1] = rgbaTopDown[i + 1];
        bgra[i + 2] = rgbaTopDown[i + 0];
        bgra[i + 3] = rgbaTopDown[i + 3];
    }
    w.Write(bgra);
}
