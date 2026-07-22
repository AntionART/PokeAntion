using System.Text.Json;
using ClientApp;
using ClientApp.Network;
using PokemonOnline.Protocol;
using RomLoader;

namespace ClientApp.Battle;

/// <summary>
/// Fase battle-4: pantalla de batalla PvP (ver server/internal/battlesession). Se activa sola
/// al recibir "battle_start" (el desafío/aceptación previo vive en SocialPanel.Tab.Battle,
/// mismo patrón row-based que trade) y toma control total del input/dibujo hasta "battle_end"
/// o "battle_cancelled" — mientras está activa, Program.cs no debe pasarle input al juego ni
/// al panel social (ver IsActive, mismo criterio que chatActive/SocialPanel.IsActive).
///
/// El servidor decide todo lo que importa (daño/orden/ganador, ver battlesession.Service) —
/// esta clase solo REFLEJA ese estado y manda la elección de movimiento del jugador. Los
/// nombres/PP de movimientos NO vienen del servidor (battle_start solo manda species/nivel/HP,
/// ver protocol.BattlePokemonPayload): se leen de la RAM real del propio jugador (getLocalParty,
/// mismo mecanismo que memAdapter.GetParty() en Program.cs) porque el cliente ya tiene esa
/// info exacta ahí — pedírsela al servidor sería duplicar una fuente de verdad que ya existe.
/// </summary>
internal sealed class BattleScreen
{
    private const int VK_UP = 0x26, VK_DOWN = 0x28, VK_RETURN = 0x0D, VK_ESCAPE = 0x1B;

    private readonly WebSocketClient _ws;
    private readonly string _myCharacterId;
    private readonly Func<IReadOnlyList<Gen3Codec.DecryptedPokemon>?> _getLocalParty;
    private readonly string _spritesRootDir;

    public bool IsActive { get; private set; }

    private string _sessionId = "";
    private BattlePokemonPayload? _yours, _opponent;
    private readonly List<string> _log = new();
    private int _selection;
    private bool _waitingForTurn;
    private string? _resultText;

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
                _sessionId = start.BattleSessionId;
                _yours = start.Yours;
                _opponent = start.Opponent;
                _log.Clear();
                _log.Add($"¡{DisplayName(_opponent)} quiere pelear!");
                _selection = 0;
                _waitingForTurn = false;
                _resultText = null;
                IsActive = true;
                break;

            case "battle_turn_result":
                var turn = payload.Deserialize<BattleTurnResultPayload>();
                if (turn == null || turn.BattleSessionId != _sessionId || _yours == null || _opponent == null) return;
                foreach (var ev in turn.Events) AppendEventLog(ev);
                _yours.CurrentHp = turn.YourHp;
                _opponent.CurrentHp = turn.OpponentHp;
                _waitingForTurn = false;
                break;

            case "battle_end":
                var end = payload.Deserialize<BattleEndPayload>();
                if (end == null || end.BattleSessionId != _sessionId) return;
                _resultText = end.YouWon ? "¡GANASTE!" : "Perdiste...";
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
        }
    }

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
                _log.Add($"{MoveCatalog.NameOf(ev.MoveId)} bajó una estadística de {(isMe ? DisplayName(_opponent) : DisplayName(_yours))}.");
                break;
            case "no_pp":
                _log.Add($"{who} no tiene PP para ese movimiento.");
                break;
        }
    }

    private static string DisplayName(BattlePokemonPayload? p) =>
        p == null ? "?" : (string.IsNullOrEmpty(p.Nickname) ? $"#{p.SpeciesId}" : p.Nickname);

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

    public void HandleInput(Win32Window window)
    {
        if (!IsActive) return;

        bool returnNow = window.IsKeyDown(VK_RETURN);
        bool escapeNow = window.IsKeyDown(VK_ESCAPE);

        if (_resultText != null)
        {
            if ((returnNow && !_prevReturn) || (escapeNow && !_prevEscape))
            {
                IsActive = false;
                _sessionId = "";
                _yours = null;
                _opponent = null;
                _resultText = null;
            }
            _prevReturn = returnNow; _prevEscape = escapeNow;
            return;
        }

        if (_waitingForTurn) { _prevReturn = returnNow; _prevEscape = escapeNow; return; }

        var moves = LocalMoves();
        bool upNow = window.IsKeyDown(VK_UP), downNow = window.IsKeyDown(VK_DOWN);
        if (moves.Length > 0)
        {
            if (upNow && !_prevUp) _selection = (_selection - 1 + moves.Length) % moves.Length;
            if (downNow && !_prevDown) _selection = (_selection + 1) % moves.Length;
        }
        if (returnNow && !_prevReturn && moves.Length > 0 && _selection < moves.Length)
        {
            _ws.SendAsync("battle_action", new BattleActionPayload { BattleSessionId = _sessionId, MoveSlot = _selection }).GetAwaiter().GetResult();
            _waitingForTurn = true;
        }

        _prevUp = upNow; _prevDown = downNow; _prevReturn = returnNow; _prevEscape = escapeNow;
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
        float mineX = 60f, mineY = windowHeight - SpriteSize - 140f;
        EnsureSpriteTexture(renderer, _yours.SpeciesId, back: true);
        renderer.AddSprite(SpriteKey(_yours.SpeciesId, back: true), mineX, mineY, SpriteSize, SpriteSize);
        renderer.AddText(DisplayName(_yours) + $"  Nv.{_yours.Level}", mineX + SpriteSize + 20f, mineY, Theme.Primary.R, Theme.Primary.G, Theme.Primary.B, 1f);
        DrawHpBar(renderer, mineX + SpriteSize + 20f, mineY + lineH * 1.3f, 200f, _yours.CurrentHp, _yours.MaxHp);

        // --- Log / menú (franja inferior, como el cuadro de texto del juego real) ---
        const float BoxX = 40f, BoxWidth = 560f;
        float boxY = windowHeight - 130f;
        renderer.AddRect(BoxX - 16f, boxY - 16f, BoxWidth, 130f, Theme.NeutralDark.R, Theme.NeutralDark.G, Theme.NeutralDark.B, 0.92f);
        renderer.AddRect(BoxX - 16f, boxY - 16f, BoxWidth, 3f, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 1f);

        float y = boxY;
        if (_resultText != null)
        {
            renderer.AddText(_resultText, BoxX, y, Theme.Tertiary.R, Theme.Tertiary.G, Theme.Tertiary.B, 1f, 1.4f);
            y += lineH * 1.8f;
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
            renderer.AddText("Esperando al rival...", BoxX, y, 0.7f, 0.7f, 1f, 1f);
            return;
        }

        var moves = LocalMoves();
        if (moves.Length == 0)
        {
            renderer.AddText("(sin movimientos disponibles)", BoxX, y, 0.6f, 0.6f, 0.6f, 0.8f);
            return;
        }
        for (int i = 0; i < moves.Length; i++)
        {
            bool sel = i == _selection;
            string prefix = sel ? "> " : "  ";
            if (sel) renderer.AddRect(BoxX - 4f, y - 1f, BoxWidth - 24f, lineH, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 0.25f);
            renderer.AddText($"{prefix}{MoveCatalog.NameOf(moves[i].MoveId)}  PP {moves[i].Pp}", BoxX, y, 0.9f, 1f, 0.9f, 1f);
            y += lineH;
        }
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
