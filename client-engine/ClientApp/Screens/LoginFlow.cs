using System.Net.Http.Json;
using System.Text.Json;
using ClientApp.Network;
using PokemonOnline.Protocol;

namespace ClientApp.Screens;

/// <summary>Resultado de un login exitoso: la conexión WebSocket ya autenticada, lista para
/// que el loop principal le enganche el handler de gameplay (player_update, chat, etc.).
/// BufferedMessages son los mensajes que el servidor mandó ENTRE el login_ok y el momento en
/// que el llamador termina de enganchar su propio handler (ver comentario en LoginFlow sobre
/// por qué existe esto) — el llamador debe reproducirlos, en orden, antes de considerar la
/// sesión "al día".</summary>
public sealed record GameSession(
    WebSocketClient Ws, string CharacterId, string MapId, int X, int Y, string SessionToken, RomCatalogEntry Rom,
    IReadOnlyList<(string Type, JsonElement Payload)> BufferedMessages, string Color);

/// <summary>
/// Pantallas previas a la partida: login, creación de cuenta y selección de ROM — todo
/// dibujado con el propio motor (Renderer.AddText, el mismo pipeline de texto del chat en
/// juego), sin ninguna librería de UI externa. Esto reemplaza el arranque anterior (cuenta
/// "client_dev" fija, ROM fija por argumento) por lo que pidió el diseño del proyecto: que el
/// motor propio tenga su propia interfaz de inicio de sesión / creación de cuenta / selección
/// de ROM antes de entrar al juego.
///
/// Navegación deliberadamente solo por teclado (Tab para moverse entre campos, Enter para
/// confirmar, Escape para volver, flechas para listas): Win32Window todavía no expone mouse,
/// y no hace falta para un formulario de unos pocos campos.
/// </summary>
internal sealed class LoginFlow
{
    private enum Screen { Login, Register, PickStarter, RomSelect, Error }

    // Los 3 iniciales de Emerald (mismos IDs que RomLoader.StarterCatalog/
    // server/internal/pokemon/starters.go) — sin esto, una cuenta creada por la UI real nunca
    // termina con un Pokémon en team_slot 0 (el servidor solo llama pokemon.AddStarter si
    // starter_species != 0, ver main.go handleRegister), y sin eso no se puede pelear/comerciar.
    // Bug real encontrado: esta pantalla existía desde antes de que el sistema de batalla
    // tuviera sentido, y nunca se actualizó para mandar starter_species — toda cuenta creada
    // por el flujo real (a diferencia de --dev-boot, que no pasa por acá) quedaba sin equipo.
    private static readonly (ushort Species, string Name)[] StarterChoices =
    [
        (RomLoader.StarterCatalog.SpeciesTreecko, "TREECKO"),
        (RomLoader.StarterCatalog.SpeciesTorchic, "TORCHIC"),
        (RomLoader.StarterCatalog.SpeciesMudkip, "MUDKIP"),
    ];

    private const int VK_UP = 0x26, VK_DOWN = 0x28, VK_RETURN = 0x0D, VK_TAB = 0x09, VK_ESCAPE = 0x1B, VK_F4 = 0x73;
    private static readonly TimeSpan LoginTimeout = TimeSpan.FromSeconds(6);

    private readonly Win32Window _window;
    private readonly Renderer _renderer;
    private readonly float _windowWidth, _windowHeight;
    private readonly string _serverHttp;
    private readonly string _serverWs;
    private readonly List<RomCatalogEntry> _catalog;

    private Screen _screen;
    private readonly TextField _loginUser = new("Usuario");
    private readonly TextField _loginPass = new("Contraseña", masked: true);
    private readonly TextField _regUser = new("Usuario");
    private readonly TextField _regEmail = new("Email");
    private readonly TextField _regPass = new("Contraseña", masked: true);
    private readonly TextField _regNick = new("Apodo");
    private int _focus;
    private int _starterIndex;
    private int _romIndex;
    private string _status = "";
    private bool _statusIsError;

    /// <summary>Volcado opcional de una captura de esta pantalla a los N frames (mismo
    /// mecanismo que --dump-frame en Program.cs, pero para verificar el render de login/
    /// registro/selección de ROM en vez del juego, en entornos sin captura de pantalla — ver
    /// nota de RDP en el README). No corta el loop: sigue funcionando después de volcar.</summary>
    public string? DumpPath { get; set; }
    public int DumpAfterFrames { get; set; } = 60;

    public LoginFlow(Win32Window window, Renderer renderer, float windowWidth, float windowHeight, string serverHttp, string serverWs, List<RomCatalogEntry> catalog, string? prefillUsername = null)
    {
        _window = window;
        _renderer = renderer;
        _windowWidth = windowWidth;
        _windowHeight = windowHeight;
        _serverHttp = serverHttp;
        _serverWs = serverWs;
        _catalog = catalog;
        _screen = catalog.Count == 0 ? Screen.Error : Screen.Login;
        if (catalog.Count == 0)
            _status = "No se encontró ninguna ROM válida en este equipo (ver memory-maps/*.json -> rom_path).";
        // "Recordar usuario" (Launcher): solo precarga el campo, la contraseña siempre se sigue
        // pidiendo acá — el Launcher nunca la ve ni la guarda.
        if (!string.IsNullOrEmpty(prefillUsername))
            _loginUser.SetValue(prefillUsername);
    }

    /// <summary>Corre el sub-loop de estas pantallas hasta completar login + selección de ROM,
    /// o hasta que se cierre la ventana (null en ese caso). No usa `await` en ningún punto —
    /// mismo motivo que el resto del cliente (Program.cs): un `await` acá podría reanudar en
    /// un hilo del thread-pool distinto del que es dueño de la ventana Win32, dejándola sorda
    /// al teclado. Las llamadas de red se hacen sincrónicas (`.GetAwaiter().GetResult()`),
    /// aceptable porque son disparadas por una tecla puntual (Enter), no un hot path.</summary>
    // El loop del juego se autolimita porque core.RunFrame() (emulación real, con costo de CPU
    // real por frame) le da un ritmo natural. Estas pantallas no tienen ningún trabajo pesado
    // por iteración, así que sin este cap el loop presenta a la GPU tan rápido como el CPU
    // pueda — en el driver de GPU virtual de este entorno (RDP sin GPU física) eso alcanzó a
    // tumbar el dispositivo D3D11 (DXGI_ERROR_DEVICE_REMOVED) en cuestión de pocos frames.
    private const double TargetFrameSeconds = 1.0 / 60.0;

    public GameSession? Run()
    {
        WebSocketClient? ws = null;
        bool prevTab = false, prevReturn = false, prevEscape = false, prevF4 = false, prevUp = false, prevDown = false;
        int frame = 0;
        var frameClock = System.Diagnostics.Stopwatch.StartNew();

        while (!_window.ShouldClose)
        {
            double frameStart = frameClock.Elapsed.TotalSeconds;
            frame++;
            _window.PumpMessages();
            if (_window.ShouldClose) break;

            // Importante: los toggles de pantalla/foco (F4, Escape, Tab) se resuelven ANTES de
            // repartir los caracteres tipeados este frame, no después. Si se repartiera antes,
            // un frame que contenga a la vez la tecla de toggle Y el primer carácter de un
            // tipeo rápido (guiones automatizados, o incluso un usuario veloz) despacharía ese
            // carácter contra el campo de la pantalla VIEJA en vez de la nueva — encontrado
            // como bug real al automatizar pruebas: un F4 (Login->Registro) seguido de tipeo
            // inmediato ocasionalmente colaba un carácter en el campo equivocado.
            bool tabNow = _window.IsKeyDown(VK_TAB);
            bool returnNow = _window.IsKeyDown(VK_RETURN);
            bool escapeNow = _window.IsKeyDown(VK_ESCAPE);
            bool f4Now = _window.IsKeyDown(VK_F4);
            bool upNow = _window.IsKeyDown(VK_UP);
            bool downNow = _window.IsKeyDown(VK_DOWN);

            var chars = _window.ConsumeTypedChars();

            if (f4Now && !prevF4 && _screen is Screen.Login or Screen.Register)
            {
                _screen = _screen == Screen.Login ? Screen.Register : Screen.Login;
                _focus = 0;
                _status = "";
                chars.Clear(); // descartar cualquier tipeo que haya llegado en el mismo frame que el toggle
            }
            if (escapeNow && !prevEscape && _screen == Screen.Register)
            {
                _screen = Screen.Login;
                _focus = 0;
                _status = "";
                chars.Clear();
            }
            if (escapeNow && !prevEscape && _screen == Screen.PickStarter)
            {
                _screen = Screen.Register;
                _status = "";
            }

            TextField[] fields = ActiveFields();
            if (tabNow && !prevTab && fields.Length > 0)
            {
                _focus = (_focus + 1) % fields.Length;
                _status = "";
                chars.Clear(); // mismo motivo: no colar el '\t' sintetizado ni nada que haya llegado junto
            }
            if (fields.Length > 0)
            {
                foreach (char c in chars) fields[_focus].HandleChar(c);
            }

            if (_screen == Screen.RomSelect && _catalog.Count > 1)
            {
                if (upNow && !prevUp) _romIndex = (_romIndex - 1 + _catalog.Count) % _catalog.Count;
                if (downNow && !prevDown) _romIndex = (_romIndex + 1) % _catalog.Count;
            }
            if (_screen == Screen.PickStarter)
            {
                if (upNow && !prevUp) _starterIndex = (_starterIndex - 1 + StarterChoices.Length) % StarterChoices.Length;
                if (downNow && !prevDown) _starterIndex = (_starterIndex + 1) % StarterChoices.Length;
            }

            if (returnNow && !prevReturn)
            {
                switch (_screen)
                {
                    case Screen.Login:
                        ws ??= TryConnect();
                        if (ws != null) TrySubmitLogin(ws);
                        if (_pendingResult != null) return _pendingResult;
                        break;
                    case Screen.Register:
                        if (_regUser.Value.Length == 0 || _regPass.Value.Length == 0)
                        {
                            SetError("Completá al menos usuario y contraseña.");
                            break;
                        }
                        _status = "";
                        _starterIndex = 0;
                        _screen = Screen.PickStarter;
                        break;
                    case Screen.PickStarter:
                        TrySubmitRegister(StarterChoices[_starterIndex].Species);
                        break;
                    case Screen.RomSelect:
                        if (ws != null && _pendingLogin != null)
                        {
                            var rom = _catalog[_romIndex];
                            var login = _pendingLogin.Value;
                            ws.OnMessage -= BufferMessage;
                            return new GameSession(ws, login.CharacterId, login.MapId, login.X, login.Y, login.SessionToken, rom, _bufferedMessages, login.Color);
                        }
                        break;
                }
            }

            prevTab = tabNow; prevReturn = returnNow; prevEscape = escapeNow; prevF4 = f4Now; prevUp = upNow; prevDown = downNow;

            _renderer.ClearText();
            Draw();
            _renderer.Render();

            if (DumpPath != null && frame == DumpAfterFrames)
            {
                _renderer.CaptureToFile(DumpPath);
                Console.WriteLine($"[login-ui] frame {frame} volcado a {DumpPath}");
                DumpPath = null;
            }

            double remaining = TargetFrameSeconds - (frameClock.Elapsed.TotalSeconds - frameStart);
            if (remaining > 0) Thread.Sleep((int)(remaining * 1000));
        }

        if (ws != null) ws.DisposeAsync().AsTask().GetAwaiter().GetResult();
        return null;
    }

    private (string CharacterId, string MapId, int X, int Y, string SessionToken, string Color)? _pendingLogin;

    // El servidor manda notificaciones (map_players_snapshot, friend_status_update, etc.)
    // apenas se completa el login — pero el jugador todavía puede tardar en elegir ROM en esta
    // misma pantalla, y el handler "de verdad" (Program.cs) recién se engancha cuando Run()
    // devuelve la sesión. Sin este buffer, todo lo que llegara mientras el jugador mira la
    // lista de ROMs se perdía en silencio (un evento de C# sin suscriptores no hace nada, no
    // encola nada) — encontrado en vivo: el snapshot de "quién ya está en tu mapa" nunca
    // llegaba al cliente real aunque el servidor sí lo mandaba (confirmado con un WebSocket
    // crudo). Se guarda tal cual llegó y Program.cs lo reproduce, en orden, apenas engancha su
    // propio handler (ver GameSession.BufferedMessages).
    private readonly List<(string Type, JsonElement Payload)> _bufferedMessages = new();
    private void BufferMessage(string type, JsonElement payload) => _bufferedMessages.Add((type, payload));

    private TextField[] ActiveFields() => _screen switch
    {
        Screen.Login => [_loginUser, _loginPass],
        Screen.Register => [_regUser, _regEmail, _regPass, _regNick],
        _ => [], // PickStarter/RomSelect navegan con flechas, no con campos de texto
    };

    private WebSocketClient? TryConnect()
    {
        try
        {
            var ws = new WebSocketClient();
            ws.OnError += ex => Console.Error.WriteLine($"[ws] error: {ex.Message}");
            ws.ConnectAsync(_serverWs).GetAwaiter().GetResult();
            // Suscribir el buffer ACÁ, antes de mandar "login" siquiera — no después del
            // login_ok. Un handler temporal que se desengancha y otro que se engancha después
            // deja una ventana de cero suscriptores entre los dos; en un servidor local (RTT de
            // microsegundos) el hilo de recepción puede procesar login_ok Y el
            // map_players_snapshot que le sigue ANTES de que el hilo principal termine de
            // ejecutar las pocas líneas de C# entre "desuscribir A" y "suscribir B" — verificado
            // en vivo con un diagnóstico: el snapshot llegaba con 0 suscriptores. Con un único
            // buffer persistente desde la conexión misma, esa ventana no existe nunca.
            ws.OnMessage += BufferMessage;
            return ws;
        }
        catch (Exception ex)
        {
            SetError($"No se pudo conectar a {_serverWs}: {ex.Message}");
            return null;
        }
    }

    private void TrySubmitLogin(WebSocketClient ws)
    {
        if (_loginUser.Value.Length == 0 || _loginPass.Value.Length == 0)
        {
            SetError("Completá usuario y contraseña.");
            return;
        }

        _status = "Conectando...";
        _statusIsError = false;
        _renderer.ClearText();
        Draw();
        _renderer.Render(); // feedback inmediato antes de bloquear en la llamada de red

        // BufferMessage ya está enganchado desde TryConnect() — LoginBlocking no necesita
        // encargarse de los mensajes que no sean login_ok/login_error, ese buffer los sigue
        // acumulando en paralelo sin ninguna ventana muerta.
        var result = LoginBlocking(ws, _loginUser.Value, _loginPass.Value, LoginTimeout);
        if (result.Error != null)
        {
            SetError(result.Error);
            return;
        }

        _pendingLogin = (result.CharacterId!, result.MapId!, result.X, result.Y, result.SessionToken!, result.Color ?? "default");

        // Catálogo de una sola entrada = el Launcher ya resolvió/validó la ROM antes de arrancar
        // este .exe (ver Program.cs --rom/--memory-map/--rom-id) — no hace falta preguntarle al
        // jugador algo que ya se decidió, el motor solo ejecuta el juego.
        if (_catalog.Count == 1)
        {
            var login = _pendingLogin.Value;
            ws.OnMessage -= BufferMessage;
            _pendingResult = new GameSession(ws, login.CharacterId, login.MapId, login.X, login.Y, login.SessionToken, _catalog[0], _bufferedMessages, login.Color);
            return;
        }

        _status = "";
        _screen = Screen.RomSelect;
        _romIndex = 0;
    }

    private GameSession? _pendingResult;

    private void TrySubmitRegister(ushort starterSpecies)
    {
        _status = "Creando cuenta...";
        _statusIsError = false;
        _renderer.ClearText();
        Draw();
        _renderer.Render();

        try
        {
            using var http = new HttpClient();
            string email = _regEmail.Value.Length > 0 ? _regEmail.Value : $"{_regUser.Value}@example.com";
            string nickname = _regNick.Value.Length > 0 ? _regNick.Value : _regUser.Value;
            var body = new
            {
                username = _regUser.Value, email, password = _regPass.Value,
                rom_id = _catalog.Count > 0 ? _catalog[0].RomId : "",
                nickname,
                starter_species = (int)starterSpecies,
            };
            var response = http.PostAsJsonAsync($"{_serverHttp}/register", body).GetAwaiter().GetResult();
            if (response.IsSuccessStatusCode)
            {
                _loginUser.SetValue(_regUser.Value);
                _loginPass.SetValue(_regPass.Value);
                _screen = Screen.Login;
                _focus = 0;
                _status = "Cuenta creada. Presioná Enter para iniciar sesión.";
                _statusIsError = false;
                return;
            }

            string body2 = response.Content.ReadAsStringAsync().GetAwaiter().GetResult();
            string reason = TryExtractError(body2) ?? $"código {(int)response.StatusCode}";
            SetError($"No se pudo crear la cuenta: {reason}");
        }
        catch (Exception ex)
        {
            SetError($"No se pudo contactar al servidor: {ex.Message}");
        }
    }

    private static string? TryExtractError(string json)
    {
        try
        {
            using var doc = JsonDocument.Parse(json);
            return doc.RootElement.TryGetProperty("error", out var e) ? e.GetString() : null;
        }
        catch
        {
            return null;
        }
    }

    private void SetError(string message)
    {
        _status = message;
        _statusIsError = true;
    }

    /// <summary>Manda "login" y espera login_ok/login_error de forma sincrónica (bloqueando
    /// con un ManualResetEventSlim en vez de `await`), reutilizable también por el arranque
    /// directo de desarrollo (--dev-boot en Program.cs). El handler se desengancha siempre en
    /// el `finally` para no seguir escuchando login_ok una vez resuelto (evita procesar dos
    /// veces si el servidor reenvía algo raro, y libera la referencia al delegate).
    ///
    /// `onOther`, si se pasa, recibe TODO mensaje que no sea login_ok/login_error mientras este
    /// método está esperando. IMPORTANTE: no alcanza por sí solo — el llamador además debe
    /// tener SU PROPIO buffer ya enganchado a ws.OnMessage desde ANTES de invocar este método
    /// (ver LoginFlow.TryConnect). El servidor puede mandar notificaciones (ej.
    /// map_players_snapshot) tan pegadas al login_ok que el hilo de recepción las procesa antes
    /// de que el hilo que espera acá siquiera despierte del `gate.Wait()` — en un servidor
    /// local, con RTT de microsegundos, esa ventana es real y se confirmó en vivo con un
    /// diagnóstico (el snapshot llegaba con cero suscriptores). `onOther` cubre el resto de la
    /// espera bloqueante una vez que ESTE handler ya está enganchado, pero el hueco ANTES de
    /// engancharlo solo lo cierra un buffer persistente desde la conexión misma.</summary>
    public static (string? CharacterId, string? MapId, int X, int Y, string? SessionToken, string? Color, string? Error) LoginBlocking(
        WebSocketClient ws, string username, string password, TimeSpan timeout, Action<string, JsonElement>? onOther = null)
    {
        using var gate = new ManualResetEventSlim(false);
        string? characterId = null, mapId = null, sessionToken = null, color = null, error = null;
        int x = 0, y = 0;

        void Handler(string type, JsonElement payload)
        {
            if (type == "login_ok")
            {
                var ok = payload.Deserialize<LoginOKPayload>();
                characterId = ok?.CharacterId;
                mapId = ok?.MapId;
                x = ok?.PosX ?? 0;
                y = ok?.PosY ?? 0;
                sessionToken = ok?.SessionToken;
                color = ok?.Color ?? "default";
                gate.Set();
            }
            else if (type == "login_error")
            {
                error = "Usuario o contraseña incorrectos.";
                gate.Set();
            }
            else
            {
                onOther?.Invoke(type, payload);
            }
        }

        ws.OnMessage += Handler;
        try
        {
            ws.SendAsync("login", new LoginPayload { Username = username, Password = password }).GetAwaiter().GetResult();
            if (!gate.Wait(timeout)) error ??= "Tiempo de espera agotado esperando respuesta del servidor.";
        }
        catch (Exception ex)
        {
            error ??= $"Error de red durante el login: {ex.Message}";
        }
        finally
        {
            ws.OnMessage -= Handler;
        }

        return (characterId, mapId, x, y, sessionToken, color, error);
    }

    // Tamaño fijo de la tarjeta central: ancho suficiente para la fila más larga (los hints de
    // Register, "Enter: continuar (elegir inicial)..."), sin depender del tamaño real de
    // ventana salvo para centrarla. Estilo "PokeMMO" pedido por el usuario: un panel enmarcado
    // sobre fondo oscuro, no texto plano flotando en negro (que era el diseño original de esta
    // pantalla, funcional pero sin ninguna intención visual).
    private const float CardWidth = 620f, CardHeight = 420f;

    private void Draw()
    {
        float cardX = (_windowWidth - CardWidth) / 2f;
        float cardY = (_windowHeight - CardHeight) / 2f;
        float lineH = _renderer.TextLineHeight;

        // Fondo de toda la ventana + la tarjeta central en sí: mismo lenguaje visual que
        // BattleScreen (rect oscuro semitransparente + barra de acento arriba), para que las
        // pantallas de inicio y de juego se sientan parte del mismo producto.
        _renderer.AddRect(0, 0, _windowWidth, _windowHeight, Theme.Background.R, Theme.Background.G, Theme.Background.B, 1f);
        _renderer.AddRect(cardX, cardY, CardWidth, CardHeight, Theme.NeutralDark.R, Theme.NeutralDark.G, Theme.NeutralDark.B, 0.96f);
        _renderer.AddRect(cardX, cardY, CardWidth, 4f, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 1f);
        _renderer.AddRect(cardX, cardY + CardHeight - 4f, CardWidth, 4f, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 1f);
        _renderer.AddRect(cardX, cardY, 4f, CardHeight, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 1f);
        _renderer.AddRect(cardX + CardWidth - 4f, cardY, 4f, CardHeight, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 1f);

        const float Pad = 32f;
        float x = cardX + Pad;
        float y = cardY + Pad;

        string title = "POKÉMON ONLINE";
        float titleScale = 1.5f;
        float titleWidth = _renderer.MeasureTextWidth(title, titleScale);
        _renderer.AddText(title, cardX + (CardWidth - titleWidth) / 2f, y, Theme.Tertiary.R, Theme.Tertiary.G, Theme.Tertiary.B, 1f, titleScale);
        y += lineH * 2.0f;
        // Línea de acento bajo el título, ancho de la tarjeta menos el padding — mismo truco de
        // "barra de color" que ya separa el header del cuerpo en BattleScreen.
        _renderer.AddRect(x, y, CardWidth - Pad * 2f, 2f, Theme.Primary.R, Theme.Primary.G, Theme.Primary.B, 0.6f);
        y += lineH * 1.2f;

        switch (_screen)
        {
            case Screen.Login:
                _renderer.AddText("Iniciar sesión", x, y, Theme.White.R, Theme.White.G, Theme.White.B, 0.95f); y += lineH * 1.6f;
                DrawField(_loginUser, x, CardWidth - Pad * 2f, ref y, focusIndex: 0);
                DrawField(_loginPass, x, CardWidth - Pad * 2f, ref y, focusIndex: 1);
                y += lineH * 0.6f;
                DrawHint("Enter: iniciar sesión   Tab: cambiar de campo   F4: crear cuenta", x, y);
                break;

            case Screen.Register:
                _renderer.AddText("Crear cuenta", x, y, Theme.White.R, Theme.White.G, Theme.White.B, 0.95f); y += lineH * 1.6f;
                DrawField(_regUser, x, CardWidth - Pad * 2f, ref y, focusIndex: 0);
                DrawField(_regEmail, x, CardWidth - Pad * 2f, ref y, focusIndex: 1);
                DrawField(_regPass, x, CardWidth - Pad * 2f, ref y, focusIndex: 2);
                DrawField(_regNick, x, CardWidth - Pad * 2f, ref y, focusIndex: 3);
                y += lineH * 0.4f;
                DrawHint("Enter: continuar (elegir inicial)   Tab: cambiar de campo   F4/Esc: volver", x, y);
                break;

            case Screen.PickStarter:
                _renderer.AddText("Elegí tu Pokémon inicial:", x, y, Theme.White.R, Theme.White.G, Theme.White.B, 0.95f); y += lineH * 1.6f;
                for (int i = 0; i < StarterChoices.Length; i++)
                    DrawListRow(StarterChoices[i].Name, i == _starterIndex, x, CardWidth - Pad * 2f, ref y);
                y += lineH * 0.6f;
                DrawHint("Arriba/Abajo: elegir   Enter: confirmar y crear cuenta   Escape: volver", x, y);
                break;

            case Screen.RomSelect:
                _renderer.AddText("Elegí tu ROM:", x, y, Theme.White.R, Theme.White.G, Theme.White.B, 0.95f); y += lineH * 1.6f;
                for (int i = 0; i < _catalog.Count; i++)
                    DrawListRow(_catalog[i].DisplayName, i == _romIndex, x, CardWidth - Pad * 2f, ref y);
                y += lineH * 0.6f;
                DrawHint("Arriba/Abajo: elegir   Enter: confirmar", x, y);
                break;

            case Screen.Error:
                _renderer.AddText("No se puede continuar:", x, y, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 1f); y += lineH * 1.6f;
                break;
        }

        y = cardY + CardHeight - Pad - lineH;
        if (_status.Length > 0)
        {
            (float r, float g, float b) = _statusIsError ? Theme.Secondary : Theme.Primary;
            _renderer.AddText(_status, x, y, r, g, b, 1f);
        }
    }

    private void DrawHint(string text, float x, float y) =>
        _renderer.AddText(text, x, y, Theme.NeutralLight.R, Theme.NeutralLight.G, Theme.NeutralLight.B, 0.75f);

    private void DrawField(TextField field, float x, float width, ref float y, int focusIndex)
    {
        bool focused = focusIndex == _focus && _screen != Screen.RomSelect;
        float lineH = _renderer.TextLineHeight;
        float boxHeight = lineH * 1.5f;

        // "Caja" de input: como AddRect no tiene un modo "solo borde", se simula dibujando un
        // rect de color de borde un poco más grande por detrás y el fondo real encima — mismo
        // patrón ya usado en el emblema/badge de BattleScreen para las filas de menú resaltadas.
        (float br, float bg, float bb) = focused ? Theme.Primary : Theme.NeutralLight;
        float borderAlpha = focused ? 1f : 0.25f;
        _renderer.AddRect(x - 2f, y - 2f, width + 4f, boxHeight + 4f, br, bg, bb, borderAlpha);
        _renderer.AddRect(x, y, width, boxHeight, Theme.Background.R, Theme.Background.G, Theme.Background.B, 0.9f);

        string cursor = focused ? "_" : "";
        (float tr, float tg, float tb) = focused ? Theme.Primary : Theme.NeutralLight;
        _renderer.AddText($"{field.Label}: {field.Display}{cursor}", x + 10f, y + boxHeight * 0.22f, tr, tg, tb, 1f);
        y += boxHeight + lineH * 0.5f;
    }

    private void DrawListRow(string label, bool selected, float x, float width, ref float y)
    {
        float lineH = _renderer.TextLineHeight;
        float rowHeight = lineH * 1.3f;
        if (selected)
            _renderer.AddRect(x - 6f, y - 2f, width + 12f, rowHeight, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 0.3f);

        (float r, float g, float b) = selected ? Theme.Primary : Theme.NeutralLight;
        _renderer.AddText((selected ? "> " : "  ") + label, x, y, r, g, b, 1f);
        y += rowHeight;
    }
}
