using System.Linq;
using System.Text.Json;
using ClientApp;
using ClientApp.Network;
using PokemonOnline.Protocol;
using RomLoader;

namespace ClientApp.Battle;

/// <summary>
/// Fase battle-4 (+ encuentros salvajes): pantalla de batalla, tanto PvP (ver
/// server/internal/battlesession) como encuentros salvajes (ver server/internal/wildencounter,
/// 100% servidor-autoritativo: el cliente nunca decide especie/nivel/captura). Se activa sola al
/// recibir "battle_start"/"wild_battle_start" y toma control total del input/dibujo hasta que
/// termina — mientras está activa, Program.cs no debe pasarle input al juego ni al panel social
/// (ver IsActive, mismo criterio que chatActive/SocialPanel.IsActive).
///
/// El servidor decide todo lo que importa (daño/orden/ganador/objetos válidos/captura) — esta
/// clase solo REFLEJA ese estado y manda la elección del jugador. Los nombres/PP de movimientos
/// del PROPIO Pokémon activo NO vienen del servidor (ver protocol.BattlePokemonPayload): se leen
/// de la RAM real del propio jugador (getLocalParty) porque el cliente ya tiene esa info exacta
/// ahí — pedírsela al servidor sería duplicar una fuente de verdad que ya existe.
///
/// Un encuentro salvaje comparte casi toda esta pantalla con PvP (sprites/HP bars/Fight/Bag/Run)
/// salvo: no hay menú "Pokémon" (wildencounter no soporta cambiar de Pokémon todavía), Bag solo
/// ofrece Poké Balls (no objetos de curación — wildencounter no los soporta todavía, limitación
/// conocida), y Run manda wild_flee en vez de battle_flee.
/// </summary>
internal sealed class BattleScreen
{
    private const int VK_UP = 0x26, VK_DOWN = 0x28, VK_RETURN = 0x0D, VK_ESCAPE = 0x1B;

    private enum Menu { Main, Fight, Bag, Pokemon, ItemTarget }

    private readonly WebSocketClient _ws;
    private readonly string _myCharacterId;
    private readonly Func<IReadOnlyList<Gen3Codec.DecryptedPokemon>?> _getLocalParty;
    private readonly string _spritesRootDir;

    public bool IsActive { get; private set; }

    private bool _isWild;
    private string _sessionId = "";
    private BattlePokemonPayload? _yours, _opponent;
    private readonly List<string> _log = new();
    private Menu _menu = Menu.Main;
    private int _selection;
    private bool _waitingForTurn;
    private string? _resultText;
    // Tu Pokémon activo se debilitó y te queda equipo vivo: battlesession exige un battle_switch
    // antes que cualquier otra cosa — mientras esto es true, no hay menú principal, solo Pokémon.
    // No aplica a encuentros salvajes (wildencounter no tiene equipo, solo un activo).
    private bool _forcedSwitch;

    private List<ItemStackPayload> _items = new();
    private List<BattlePokemonPayload> _team = new();
    private int _activeTeamIndex;
    // Objeto elegido en Bag esperando a quién aplicarlo (ver Menu.ItemTarget) — null fuera de ese
    // submenú. Solo se usa cuando hay más de un destino elegible; con uno solo se manda directo,
    // sin preguntar (ver HandleBagInput).
    private ItemStackPayload? _pendingItem;

    private bool _prevUp, _prevDown, _prevReturn, _prevEscape;

    public BattleScreen(WebSocketClient ws, string myCharacterId,
        Func<IReadOnlyList<Gen3Codec.DecryptedPokemon>?> getLocalParty, string spritesRootDir)
    {
        _ws = ws;
        _myCharacterId = myCharacterId;
        _getLocalParty = getLocalParty;
        _spritesRootDir = spritesRootDir;
    }

    /// <summary>Procesa mensajes del servidor relevantes para esta pantalla. Se llama desde el
    /// handler general de gameplay en Program.cs, igual que SocialPanel.HandleMessage — ambos
    /// reciben TODOS los mensajes y cada uno ignora los que no le interesan.</summary>
    public void HandleMessage(string type, JsonElement payload)
    {
        switch (type)
        {
            case "battle_start":
                var start = payload.Deserialize<BattleStartPayload>();
                if (start == null) return;
                _isWild = false;
                _sessionId = start.BattleSessionId;
                _yours = start.Yours;
                _opponent = start.Opponent;
                ResetForNewBattle($"¡{DisplayName(_opponent)} quiere pelear!");
                break;

            case "wild_battle_start":
                var wildStart = payload.Deserialize<WildBattleStartPayload>();
                if (wildStart == null) return;
                _isWild = true;
                _sessionId = wildStart.SessionId;
                _yours = wildStart.Yours;
                _opponent = new BattlePokemonPayload
                {
                    PokemonId = "", SpeciesId = wildStart.Wild.SpeciesId, Nickname = null,
                    Level = wildStart.Wild.Level, CurrentHp = wildStart.Wild.CurrentHp, MaxHp = wildStart.Wild.MaxHp,
                };
                ResetForNewBattle($"¡Un {DisplayName(_opponent)} salvaje apareció!");
                break;

            case "battle_turn_result":
                var turn = payload.Deserialize<BattleTurnResultPayload>();
                if (turn == null || turn.BattleSessionId != _sessionId || _yours == null || _opponent == null) return;
                foreach (var ev in turn.Events) AppendEventLog(ev);
                _yours.CurrentHp = turn.YourHp;
                _opponent.CurrentHp = turn.OpponentHp;
                _waitingForTurn = false;
                _selection = 0;
                if (turn.YouMustSwitch)
                {
                    _forcedSwitch = true;
                    _menu = Menu.Pokemon;
                    RequestTeam();
                }
                else
                {
                    _forcedSwitch = false;
                    _menu = Menu.Main;
                }
                break;

            case "wild_turn_result":
                var wildTurn = payload.Deserialize<WildTurnResultPayload>();
                if (wildTurn == null || wildTurn.SessionId != _sessionId || _yours == null || _opponent == null) return;
                foreach (var ev in wildTurn.Events)
                {
                    AppendEventLog(new BattleEventPayload
                    {
                        Type = ev.Type, ActorCharacterId = ev.IsPlayer ? _myCharacterId : "",
                        MoveId = ev.MoveId, Damage = ev.Damage, Effectiveness = ev.Effectiveness, Fainted = ev.Fainted, Amount = ev.Amount,
                    });
                }
                _yours.CurrentHp = wildTurn.YourHp;
                _opponent.CurrentHp = wildTurn.WildHp;
                _waitingForTurn = false;
                _selection = 0;
                _menu = Menu.Main;
                break;

            case "battle_end":
                var end = payload.Deserialize<BattleEndPayload>();
                if (end == null || end.BattleSessionId != _sessionId) return;
                _resultText = end.YouWon
                    ? (end.Reason == "fled" ? "El rival huyó. ¡Ganaste!" : "¡GANASTE!")
                    : (end.Reason == "fled" ? "Huiste de la batalla." : "Perdiste...");
                _waitingForTurn = false;
                break;

            case "wild_battle_end":
                var wildEnd = payload.Deserialize<WildBattleEndPayload>();
                if (wildEnd == null || wildEnd.SessionId != _sessionId) return;
                _resultText = wildEnd.Reason switch
                {
                    "caught" => wildEnd.CaughtPokemon != null
                        ? $"¡Atrapaste a {SpeciesName(wildEnd.CaughtPokemon.SpeciesId)}!"
                        : "¡Atrapado!",
                    "wild_fainted" => wildEnd.LeveledUp
                        ? $"¡Ganaste! +{wildEnd.ExpGained} EXP. ¡Subió a Nv.{wildEnd.NewLevel}!"
                        : $"¡Ganaste! +{wildEnd.ExpGained} EXP.",
                    "player_fainted" => "Tu Pokémon se debilitó...",
                    "fled" => "Huiste del encuentro.",
                    _ => "El encuentro terminó.",
                };
                if (wildEnd.LearnedMoveIds.Length > 0 && _yours != null)
                {
                    string moveNames = string.Join(", ", wildEnd.LearnedMoveIds.Select(MoveCatalog.NameOf));
                    _resultText += $"\n¡{DisplayName(_yours)} aprendió {moveNames}!";
                }
                _waitingForTurn = false;
                break;

            case "battle_cancelled":
                var cancelled = payload.Deserialize<BattleCancelledPayload>();
                if (cancelled != null && cancelled.BattleSessionId != "" && cancelled.BattleSessionId != _sessionId) return;
                IsActive = false;
                _sessionId = "";
                _yours = null;
                _opponent = null;
                break;

            case "battle_team":
                var teamPayload = payload.Deserialize<BattleTeamPayload>();
                if (teamPayload == null || teamPayload.BattleSessionId != _sessionId) return;
                _team = new List<BattlePokemonPayload>(teamPayload.Team);
                _activeTeamIndex = teamPayload.ActiveIndex;
                break;

            case "my_item_list":
                var itemList = payload.Deserialize<MyItemListPayload>();
                if (itemList != null) _items = new List<ItemStackPayload>(itemList.Items);
                break;
        }
    }

    private void ResetForNewBattle(string openingLine)
    {
        _log.Clear();
        _log.Add(openingLine);
        _menu = Menu.Main;
        _selection = 0;
        _waitingForTurn = false;
        _forcedSwitch = false;
        _resultText = null;
        _items.Clear();
        _team.Clear();
        _pendingItem = null;
        IsActive = true;
    }

    private void RequestTeam() => _ws.SendAsync("battle_team_request", new BattleTeamRequestPayload { BattleSessionId = _sessionId }).GetAwaiter().GetResult();
    private void RequestItems() => _ws.SendAsync("list_my_items", new { }).GetAwaiter().GetResult();

    private void AppendEventLog(BattleEventPayload ev)
    {
        bool isMe = ev.ActorCharacterId == _myCharacterId;
        string who = isMe ? DisplayName(_yours) : DisplayName(_opponent);
        switch (ev.Type)
        {
            case "damage":
                string effectText = ev.Effectiveness switch
                {
                    > 1.5 => " ¡Es supereficaz!",
                    < 1.0 and > 0 => " No es muy eficaz...",
                    0 => " No tuvo efecto...",
                    _ => "",
                };
                _log.Add($"{who} usó {MoveCatalog.NameOf(ev.MoveId)}. {ev.Damage} de daño.{effectText}");
                if (ev.Fainted) _log.Add($"¡{(isMe ? DisplayName(_yours) : DisplayName(_opponent))} se debilitó!");
                break;
            case "miss":
                _log.Add($"{who} usó {MoveCatalog.NameOf(ev.MoveId)}, pero falló.");
                break;
            case "stat_change":
                string dir = ev.Amount > 0 ? "subió" : "bajó";
                _log.Add($"¡{dir} una estadística de {ev.TargetNickname ?? SpeciesDisplay(ev.TargetSpecies)}!");
                break;
            case "no_pp":
                _log.Add($"{who} no tiene PP para ese movimiento.");
                break;
            case "no_effect":
                _log.Add($"{who} usó {MoveCatalog.NameOf(ev.MoveId)}, pero no tuvo ningún efecto.");
                break;
            case "switch":
                _log.Add($"{(isMe ? "Volvé" : $"{DisplayName(_opponent)} volvé")}! {who} envía a {ev.TargetNickname ?? SpeciesDisplay(ev.TargetSpecies)}.");
                break;
            case "item_used":
                _log.Add($"{who} usó un objeto en {ev.TargetNickname ?? SpeciesDisplay(ev.TargetSpecies)}.");
                break;
        }
    }

    private static string SpeciesDisplay(int species) => species > 0 ? $"#{species}" : "?";

    private static string SpeciesName(int species) => PokedexCatalog.Species(species)?.Name ?? SpeciesDisplay(species);

    private static string DisplayName(BattlePokemonPayload? p) =>
        p == null ? "?" : (string.IsNullOrEmpty(p.Nickname) ? SpeciesName(p.SpeciesId) : p.Nickname);

    /// <summary>Movimientos+PP del propio Pokémon activo (slot 0), leídos de la RAM real — ver
    /// comentario de clase. Vacío si el emulador no tiene el equipo listo todavía (ej.
    /// --mock-data, o un frame muy temprano tras el login).</summary>
    private (int MoveId, int Pp)[] LocalMoves()
    {
        var party = _getLocalParty();
        if (party == null || party.Count == 0) return [];
        var mon = party[0];
        var ids = mon.MoveIds();
        var pps = mon.PPs();
        var moves = new List<(int, int)>();
        for (int i = 0; i < 4; i++)
            if (ids[i] != 0) moves.Add((ids[i], pps[i]));
        return moves.ToArray();
    }

    // IDs reales de include/constants/items.h (ver server/internal/inventory.Catalog) — el
    // cliente solo necesita distinguir Ball/Revive del resto para decidir a quién apuntar o qué
    // mensaje mandar, no todo el catálogo de efectos.
    private const int ItemRevive = 24, ItemMaxRevive = 25;
    private const int ItemMasterBall = 1, ItemUltraBall = 2, ItemGreatBall = 3, ItemPokeBall = 4;
    private static bool IsBallItem(int itemId) => itemId is ItemMasterBall or ItemUltraBall or ItemGreatBall or ItemPokeBall;

    /// <summary>En un encuentro salvaje, Bag solo puede tirar Poké Balls (wildencounter no
    /// soporta objetos de curación todavía, limitación conocida) — en PvP, todo lo que no sea
    /// una Ball (battlesession las rechaza explícitamente, no tiene sentido atrapar a otro
    /// jugador).</summary>
    private List<ItemStackPayload> BagItemsForCurrentMode() =>
        _items.Where(i => IsBallItem(i.ItemId) == _isWild).ToList();

    private const int MainMenuCount = 4; // Fight, Bag, Pokémon, Run — Pokémon se omite en modo salvaje (ver DrawMainMenu/HandleMainMenuInput)

    public void HandleInput(Win32Window window)
    {
        if (!IsActive) return;

        bool returnNow = window.IsKeyDown(VK_RETURN);
        bool escapeNow = window.IsKeyDown(VK_ESCAPE);
        bool upNow = window.IsKeyDown(VK_UP), downNow = window.IsKeyDown(VK_DOWN);
        bool upEdge = upNow && !_prevUp, downEdge = downNow && !_prevDown;
        bool returnEdge = returnNow && !_prevReturn, escapeEdge = escapeNow && !_prevEscape;
        _prevUp = upNow; _prevDown = downNow; _prevReturn = returnNow; _prevEscape = escapeNow;

        if (_resultText != null)
        {
            if (returnEdge || escapeEdge)
            {
                IsActive = false;
                _sessionId = "";
                _yours = null;
                _opponent = null;
                _resultText = null;
            }
            return;
        }

        if (_waitingForTurn) return;

        switch (_menu)
        {
            case Menu.Main:
                HandleMainMenuInput(upEdge, downEdge, returnEdge);
                break;
            case Menu.Fight:
                HandleFightInput(upEdge, downEdge, returnEdge, escapeEdge);
                break;
            case Menu.Bag:
                HandleBagInput(upEdge, downEdge, returnEdge, escapeEdge);
                break;
            case Menu.Pokemon:
                HandlePokemonInput(upEdge, downEdge, returnEdge, escapeEdge);
                break;
            case Menu.ItemTarget:
                HandleItemTargetInput(upEdge, downEdge, returnEdge, escapeEdge);
                break;
        }
    }

    private int MainMenuOptionCount => _isWild ? MainMenuCount - 1 : MainMenuCount; // sin "Pokémon" en modo salvaje

    private void HandleMainMenuInput(bool upEdge, bool downEdge, bool returnEdge)
    {
        int count = MainMenuOptionCount;
        if (upEdge) _selection = (_selection - 1 + count) % count;
        if (downEdge) _selection = (_selection + 1) % count;
        if (!returnEdge) return;

        // Modo salvaje: 0=Luchar, 1=Mochila, 2=Huir (sin Pokémon). PvP: 0=Luchar, 1=Mochila,
        // 2=Pokémon, 3=Huir.
        bool isRun = _selection == count - 1;
        if (_selection == 0) { _menu = Menu.Fight; _selection = 0; }
        else if (_selection == 1) { _menu = Menu.Bag; _selection = 0; RequestItems(); if (!_isWild) RequestTeam(); }
        else if (!_isWild && _selection == 2) { _menu = Menu.Pokemon; _selection = 0; RequestTeam(); }
        else if (isRun)
        {
            if (_isWild) _ws.SendAsync("wild_flee", new WildSessionRefPayload { SessionId = _sessionId }).GetAwaiter().GetResult();
            else _ws.SendAsync("battle_flee", new BattleSessionRefPayload { BattleSessionId = _sessionId }).GetAwaiter().GetResult();
        }
    }

    private void HandleFightInput(bool upEdge, bool downEdge, bool returnEdge, bool escapeEdge)
    {
        if (escapeEdge) { _menu = Menu.Main; _selection = 0; return; }

        var moves = LocalMoves();
        if (moves.Length > 0)
        {
            if (upEdge) _selection = (_selection - 1 + moves.Length) % moves.Length;
            if (downEdge) _selection = (_selection + 1) % moves.Length;
        }
        if (!returnEdge || moves.Length == 0 || _selection >= moves.Length) return;

        if (_isWild) _ws.SendAsync("wild_action", new WildActionPayload { SessionId = _sessionId, MoveSlot = _selection }).GetAwaiter().GetResult();
        else _ws.SendAsync("battle_action", new BattleActionPayload { BattleSessionId = _sessionId, MoveSlot = _selection }).GetAwaiter().GetResult();
        _waitingForTurn = true;
    }

    private void HandleBagInput(bool upEdge, bool downEdge, bool returnEdge, bool escapeEdge)
    {
        if (escapeEdge) { _menu = Menu.Main; _selection = 0; return; }
        var items = BagItemsForCurrentMode();
        if (items.Count == 0) return;

        if (upEdge) _selection = (_selection - 1 + items.Count) % items.Count;
        if (downEdge) _selection = (_selection + 1) % items.Count;
        if (!returnEdge || _selection >= items.Count) return;

        var item = items[_selection];
        if (_isWild)
        {
            _ws.SendAsync("wild_throw_ball", new WildThrowBallPayload { SessionId = _sessionId, ItemId = item.ItemId }).GetAwaiter().GetResult();
            _waitingForTurn = true;
            _menu = Menu.Main;
            return;
        }

        // A quién aplicarlo: revivir solo puede ir a un debilitado, curar solo a alguien vivo.
        // Con un solo destino elegible se manda directo (mismo comportamiento de siempre, sin
        // preguntar algo que no tiene otra respuesta posible); con 2+ se abre un submenú para
        // elegir — antes SIEMPRE iba al activo (o al primer debilitado), sin importar cuántos
        // miembros del equipo calificaban.
        bool isRevive = item.ItemId is ItemRevive or ItemMaxRevive;
        var eligible = new List<int>();
        for (int i = 0; i < _team.Count; i++)
        {
            bool ok = isRevive ? _team[i].CurrentHp <= 0 : _team[i].CurrentHp > 0;
            if (ok) eligible.Add(i);
        }
        if (eligible.Count == 0) return; // revivir sin nadie debilitado: no hay a quién, no hacer nada

        if (eligible.Count == 1)
        {
            SendBattleItem(item.ItemId, eligible[0]);
            return;
        }

        _pendingItem = item;
        _menu = Menu.ItemTarget;
        _selection = 0;
    }

    private void SendBattleItem(int itemId, int targetSlot)
    {
        _ws.SendAsync("battle_item", new BattleItemPayload { BattleSessionId = _sessionId, ItemId = itemId, TeamSlot = targetSlot }).GetAwaiter().GetResult();
        _waitingForTurn = true;
        _menu = Menu.Main;
    }

    private void HandleItemTargetInput(bool upEdge, bool downEdge, bool returnEdge, bool escapeEdge)
    {
        // Escape vuelve a Bag (no a Main): el jugador seguramente quiere elegir OTRO objeto o
        // cancelar el uso, no perder el paso de haber abierto la mochila.
        if (escapeEdge) { _menu = Menu.Bag; _selection = 0; _pendingItem = null; return; }
        if (_team.Count == 0 || _pendingItem == null) return;

        if (upEdge) _selection = (_selection - 1 + _team.Count) % _team.Count;
        if (downEdge) _selection = (_selection + 1) % _team.Count;
        if (!returnEdge || _selection >= _team.Count) return;

        bool isRevive = _pendingItem.ItemId is ItemRevive or ItemMaxRevive;
        bool eligible = isRevive ? _team[_selection].CurrentHp <= 0 : _team[_selection].CurrentHp > 0;
        if (!eligible) return; // no se puede revivir a alguien vivo ni curar a alguien debilitado

        int itemId = _pendingItem.ItemId;
        _pendingItem = null;
        SendBattleItem(itemId, _selection);
    }

    // Alto del cuadro de log/menú inferior — compartido entre Draw (tamaño del cuadro) y el
    // posicionamiento del sprite propio (para que no se solapen, ver Draw).
    private const float MessageBoxHeight = 175f;

    private void HandlePokemonInput(bool upEdge, bool downEdge, bool returnEdge, bool escapeEdge)
    {
        if (!_forcedSwitch && escapeEdge) { _menu = Menu.Main; _selection = 0; return; }
        if (_team.Count == 0) return;

        if (upEdge) _selection = (_selection - 1 + _team.Count) % _team.Count;
        if (downEdge) _selection = (_selection + 1) % _team.Count;
        if (!returnEdge || _selection >= _team.Count) return;
        if (_selection == _activeTeamIndex || _team[_selection].CurrentHp <= 0) return; // no se puede elegir al ya activo ni a uno debilitado

        _ws.SendAsync("battle_switch", new BattleSwitchPayload { BattleSessionId = _sessionId, TeamSlot = _selection }).GetAwaiter().GetResult();
        _waitingForTurn = true;
        _forcedSwitch = false;
        _menu = Menu.Main;
    }

    private static string SpriteKey(int species, bool back) => $"pkmn_{(back ? "back" : "front")}_{species}";

    private void EnsureSpriteTexture(Renderer renderer, int species, bool back)
    {
        string key = SpriteKey(species, back);
        if (renderer.HasUiTexture(key)) return;
        var loaded = back ? BattleSpriteAssets.LoadBack(_spritesRootDir, species) : BattleSpriteAssets.LoadFront(_spritesRootDir, species);
        if (loaded == null) return;
        var (rgba, w, h) = loaded.Value;
        renderer.EnsureUiTexture(key, rgba, w, h);
    }

    public void Draw(Renderer renderer, float windowWidth, float windowHeight)
    {
        if (!IsActive || _yours == null || _opponent == null) return;

        float lineH = renderer.TextLineHeight;

        // Fondo: tarjeta oscura ocupando toda la pantalla (a diferencia de SocialPanel, que es
        // un panel angosto a la izquierda — una batalla reemplaza la vista, no la comparte).
        renderer.AddRect(0, 0, windowWidth, windowHeight, Theme.Background.R, Theme.Background.G, Theme.Background.B, 0.94f);

        // --- Rival (arriba, sprite de frente) ---
        const float SpriteSize = 96f;
        float oppX = windowWidth - 220f, oppY = 40f;
        EnsureSpriteTexture(renderer, _opponent.SpeciesId, back: false);
        renderer.AddSprite(SpriteKey(_opponent.SpeciesId, back: false), oppX, oppY, SpriteSize, SpriteSize);
        renderer.AddText(DisplayName(_opponent) + $"  Nv.{_opponent.Level}", oppX - 160f, oppY, Theme.Primary.R, Theme.Primary.G, Theme.Primary.B, 1f);
        DrawHpBar(renderer, oppX - 160f, oppY + lineH * 1.3f, 200f, _opponent.CurrentHp, _opponent.MaxHp);

        // --- Tuyo (abajo, sprite de espaldas) ---
        float mineX = 60f, mineY = windowHeight - SpriteSize - MessageBoxHeight - 20f;
        EnsureSpriteTexture(renderer, _yours.SpeciesId, back: true);
        renderer.AddSprite(SpriteKey(_yours.SpeciesId, back: true), mineX, mineY, SpriteSize, SpriteSize);
        renderer.AddText(DisplayName(_yours) + $"  Nv.{_yours.Level}", mineX + SpriteSize + 20f, mineY, Theme.Primary.R, Theme.Primary.G, Theme.Primary.B, 1f);
        DrawHpBar(renderer, mineX + SpriteSize + 20f, mineY + lineH * 1.3f, 200f, _yours.CurrentHp, _yours.MaxHp);

        // --- Log / menú (franja inferior, como el cuadro de texto del juego real) ---
        // Alto suficiente para 2 líneas de log + 4 opciones del menú principal (Luchar/Mochila/
        // Pokémon/Huir) — con el alto viejo (130) "Huir" quedaba cortado contra el borde de la
        // ventana (bug real, encontrado en vivo con --debug-battle).
        const float BoxX = 40f, BoxWidth = 560f;
        float boxY = windowHeight - MessageBoxHeight;
        renderer.AddRect(BoxX - 16f, boxY - 16f, BoxWidth, MessageBoxHeight, Theme.NeutralDark.R, Theme.NeutralDark.G, Theme.NeutralDark.B, 0.92f);
        renderer.AddRect(BoxX - 16f, boxY - 16f, BoxWidth, 3f, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 1f);

        float y = boxY;
        if (_resultText != null)
        {
            // AddText no soporta '\n' (lo ignora silenciosamente, ver su comentario) — partir
            // acá en líneas reales es responsabilidad del llamador, como en todo el resto de
            // esta pantalla (log de eventos, menús). Solo la primera línea (el resultado
            // principal) usa la escala grande; el resto (ej. "X aprendió Y!") va a escala normal.
            string[] lines = _resultText.Split('\n');
            for (int i = 0; i < lines.Length; i++)
            {
                float scale = i == 0 ? 1.4f : 1f;
                renderer.AddText(lines[i], BoxX, y, Theme.Tertiary.R, Theme.Tertiary.G, Theme.Tertiary.B, 1f, scale);
                y += lineH * (i == 0 ? 1.8f : 1.2f);
            }
            renderer.AddText("Enter/Escape: cerrar", BoxX, y, 0.6f, 0.6f, 0.6f, 0.8f);
            return;
        }

        // Últimas líneas del log (más recientes primero no hace falta: se lee en orden).
        int shown = Math.Min(3, _log.Count);
        for (int i = _log.Count - shown; i < _log.Count; i++)
        {
            renderer.AddText(_log[i], BoxX, y, 0.9f, 0.9f, 0.9f, 1f);
            y += lineH;
        }
        y += lineH * 0.3f;

        if (_waitingForTurn)
        {
            renderer.AddText(_isWild ? "..." : "Esperando al rival...", BoxX, y, 0.7f, 0.7f, 1f, 1f);
            return;
        }

        switch (_menu)
        {
            case Menu.Main: DrawMainMenu(renderer, BoxX, BoxWidth, lineH, ref y); break;
            case Menu.Fight: DrawFightMenu(renderer, BoxX, BoxWidth, lineH, ref y); break;
            case Menu.Bag: DrawBagMenu(renderer, BoxX, BoxWidth, lineH, ref y); break;
            case Menu.Pokemon: DrawPokemonMenu(renderer, BoxX, BoxWidth, lineH, ref y); break;
            case Menu.ItemTarget: DrawItemTargetMenu(renderer, BoxX, BoxWidth, lineH, ref y); break;
        }
    }

    private void DrawMainMenu(Renderer renderer, float x, float width, float lineH, ref float y)
    {
        string[] options = _isWild ? ["Luchar", "Mochila", "Huir"] : ["Luchar", "Mochila", "Pokémon", "Huir"];
        for (int i = 0; i < options.Length; i++)
        {
            bool sel = i == _selection;
            if (sel) renderer.AddRect(x - 4f, y - 1f, width - 24f, lineH, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 0.25f);
            renderer.AddText($"{(sel ? "> " : "  ")}{options[i]}", x, y, 0.9f, 1f, 0.9f, 1f);
            y += lineH;
        }
    }

    private void DrawFightMenu(Renderer renderer, float x, float width, float lineH, ref float y)
    {
        var moves = LocalMoves();
        if (moves.Length == 0)
        {
            renderer.AddText("(sin movimientos disponibles)", x, y, 0.6f, 0.6f, 0.6f, 0.8f);
            return;
        }
        for (int i = 0; i < moves.Length; i++)
        {
            bool sel = i == _selection;
            if (sel) renderer.AddRect(x - 4f, y - 1f, width - 24f, lineH, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 0.25f);
            renderer.AddText($"{(sel ? "> " : "  ")}{MoveCatalog.NameOf(moves[i].MoveId)}  PP {moves[i].Pp}", x, y, 0.9f, 1f, 0.9f, 1f);
            y += lineH;
        }
        renderer.AddText("Escape: volver", x, y, 0.6f, 0.6f, 0.6f, 0.8f);
    }

    private void DrawBagMenu(Renderer renderer, float x, float width, float lineH, ref float y)
    {
        var items = BagItemsForCurrentMode();
        if (items.Count == 0)
        {
            renderer.AddText(_isWild ? "(no tenés Poké Balls)" : "(no tenés objetos)", x, y, 0.6f, 0.6f, 0.6f, 0.8f);
            y += lineH;
            renderer.AddText("Escape: volver", x, y, 0.6f, 0.6f, 0.6f, 0.8f);
            return;
        }
        for (int i = 0; i < items.Count; i++)
        {
            bool sel = i == _selection;
            if (sel) renderer.AddRect(x - 4f, y - 1f, width - 24f, lineH, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 0.25f);
            renderer.AddText($"{(sel ? "> " : "  ")}{items[i].Name} x{items[i].Quantity}", x, y, 0.9f, 1f, 0.9f, 1f);
            y += lineH;
        }
        renderer.AddText("Escape: volver", x, y, 0.6f, 0.6f, 0.6f, 0.8f);
    }

    private void DrawPokemonMenu(Renderer renderer, float x, float width, float lineH, ref float y)
    {
        if (_forcedSwitch)
        {
            renderer.AddText("¡Tu Pokémon se debilitó! Elegí un reemplazo:", x, y, Theme.Tertiary.R, Theme.Tertiary.G, Theme.Tertiary.B, 1f);
            y += lineH;
        }
        if (_team.Count == 0)
        {
            renderer.AddText("(cargando equipo...)", x, y, 0.6f, 0.6f, 0.6f, 0.8f);
            return;
        }
        for (int i = 0; i < _team.Count; i++)
        {
            var p = _team[i];
            bool sel = i == _selection;
            bool disabled = i == _activeTeamIndex || p.CurrentHp <= 0;
            string suffix = i == _activeTeamIndex ? " (en combate)" : p.CurrentHp <= 0 ? " (debilitado)" : "";
            (float r, float g, float b) = disabled ? (0.5f, 0.5f, 0.5f) : (0.9f, 1f, 0.9f);
            if (sel) renderer.AddRect(x - 4f, y - 1f, width - 24f, lineH, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 0.25f);
            renderer.AddText($"{(sel ? "> " : "  ")}{DisplayName(p)} Nv.{p.Level} {p.CurrentHp}/{p.MaxHp}{suffix}", x, y, r, g, b, 1f);
            y += lineH;
        }
        if (!_forcedSwitch) renderer.AddText("Escape: volver", x, y, 0.6f, 0.6f, 0.6f, 0.8f);
    }

    private void DrawItemTargetMenu(Renderer renderer, float x, float width, float lineH, ref float y)
    {
        if (_pendingItem == null || _team.Count == 0) return;
        bool isRevive = _pendingItem.ItemId is ItemRevive or ItemMaxRevive;

        renderer.AddText($"¿A quién usarle {_pendingItem.Name}?", x, y, Theme.Tertiary.R, Theme.Tertiary.G, Theme.Tertiary.B, 1f);
        y += lineH;

        for (int i = 0; i < _team.Count; i++)
        {
            var p = _team[i];
            bool sel = i == _selection;
            bool eligible = isRevive ? p.CurrentHp <= 0 : p.CurrentHp > 0;
            string suffix = p.CurrentHp <= 0 ? " (debilitado)" : "";
            (float r, float g, float b) = eligible ? (0.9f, 1f, 0.9f) : (0.5f, 0.5f, 0.5f);
            if (sel) renderer.AddRect(x - 4f, y - 1f, width - 24f, lineH, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 0.25f);
            renderer.AddText($"{(sel ? "> " : "  ")}{DisplayName(p)} Nv.{p.Level} {p.CurrentHp}/{p.MaxHp}{suffix}", x, y, r, g, b, 1f);
            y += lineH;
        }
        renderer.AddText("Escape: volver a la mochila", x, y, 0.6f, 0.6f, 0.6f, 0.8f);
    }

    private static void DrawHpBar(Renderer renderer, float x, float y, float width, int currentHp, int maxHp)
    {
        const float Height = 10f;
        renderer.AddRect(x, y, width, Height, 0.15f, 0.15f, 0.15f, 0.9f);
        if (maxHp <= 0) return;
        float ratio = Math.Clamp(currentHp / (float)maxHp, 0f, 1f);
        // Convención clásica de la franquicia: verde > 50%, amarillo 20-50%, rojo < 20%.
        var (r, g, b) = ratio switch
        {
            > 0.5f => (0.2f, 0.9f, 0.3f),
            > 0.2f => (0.95f, 0.85f, 0.2f),
            _ => (0.9f, 0.2f, 0.2f),
        };
        renderer.AddRect(x, y, width * ratio, Height, (float)r, (float)g, (float)b, 1f);
    }
}
