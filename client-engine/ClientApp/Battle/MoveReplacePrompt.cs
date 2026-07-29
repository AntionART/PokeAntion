using System.Text.Json;
using ClientApp.Network;
using PokemonOnline.Protocol;

namespace ClientApp.Battle;

/// <summary>
/// Pantalla modal chica: aparece cuando un Pokémon subió de nivel en un encuentro salvaje y
/// quiere aprender un movimiento nuevo pero ya tiene 4 (ver server/internal/wildencounter,
/// pokemon.LearnMove) — el servidor manda "wild_move_replace_prompt" aparte de
/// "wild_battle_end" (no bloquea el fin de la pelea) y el jugador elige acá a cuál de los 4
/// movimientos actuales reemplazar, o declinar. Independiente de BattleScreen a propósito: para
/// cuando este prompt llega, la pelea ya terminó — es una decisión de POST-batalla, no parte de
/// ella (mismo criterio que el juego real: "¿Quieres que Fulanito aprenda X?" aparece con la
/// pelea ya resuelta, después de la pantalla de "¡Ganaste!").
/// </summary>
internal sealed class MoveReplacePrompt
{
    private const int VK_UP = 0x26, VK_DOWN = 0x28, VK_RETURN = 0x0D, VK_ESCAPE = 0x1B;

    private readonly WebSocketClient _ws;
    private readonly Queue<WildMoveReplacePromptPayload> _queue = new();
    private WildMoveReplacePromptPayload? _current;
    private int _selection;
    private bool _prevUp, _prevDown, _prevReturn, _prevEscape;

    public bool IsActive => _current != null;

    public MoveReplacePrompt(WebSocketClient ws)
    {
        _ws = ws;
    }

    public void HandleMessage(string type, JsonElement payload)
    {
        if (type != "wild_move_replace_prompt") return;
        var p = payload.Deserialize<WildMoveReplacePromptPayload>();
        if (p != null) _queue.Enqueue(p);
    }

    /// <summary>Llamar una vez por frame desde el loop principal, SOLO cuando no hay otra
    /// pantalla modal ya activa (BattleScreen/SocialPanel/chat) — así el prompt espera su turno
    /// en vez de competir por el teclado con la pantalla de "¡Ganaste!" que todavía puede estar
    /// abierta cuando este mensaje llega (llegan casi pegados: wild_battle_end y este prompt se
    /// mandan en la misma respuesta del servidor).</summary>
    public void Tick()
    {
        if (_current == null && _queue.Count > 0)
        {
            _current = _queue.Dequeue();
            _selection = 0;
        }
    }

    public void HandleInput(Win32Window window)
    {
        if (_current == null) return;

        bool upNow = window.IsKeyDown(VK_UP), downNow = window.IsKeyDown(VK_DOWN);
        bool returnNow = window.IsKeyDown(VK_RETURN), escapeNow = window.IsKeyDown(VK_ESCAPE);
        bool upEdge = upNow && !_prevUp, downEdge = downNow && !_prevDown;
        bool returnEdge = returnNow && !_prevReturn, escapeEdge = escapeNow && !_prevEscape;
        _prevUp = upNow; _prevDown = downNow; _prevReturn = returnNow; _prevEscape = escapeNow;

        // 5 opciones: los 4 movimientos actuales (reemplazar) + "No aprender".
        const int OptionCount = 5;
        if (upEdge) _selection = (_selection - 1 + OptionCount) % OptionCount;
        if (downEdge) _selection = (_selection + 1) % OptionCount;

        if (escapeEdge)
        {
            Send(-1);
            return;
        }
        if (returnEdge)
        {
            Send(_selection < 4 ? _selection : -1);
        }
    }

    private void Send(int replaceSlot)
    {
        if (_current == null) return;
        _ws.SendAsync("learn_move_decision", new LearnMoveDecisionPayload
        {
            PokemonId = _current.PokemonId, NewMoveId = _current.NewMoveId, ReplaceSlot = replaceSlot,
        }).GetAwaiter().GetResult();
        _current = null;
    }

    public void Draw(Renderer renderer, float windowWidth, float windowHeight)
    {
        if (_current == null) return;

        float lineH = renderer.TextLineHeight;
        const float BoxWidth = 560f, BoxHeight = 220f;
        float boxX = (windowWidth - BoxWidth) / 2f, boxY = (windowHeight - BoxHeight) / 2f;

        renderer.AddRect(0, 0, windowWidth, windowHeight, Theme.Background.R, Theme.Background.G, Theme.Background.B, 0.85f);
        renderer.AddRect(boxX, boxY, BoxWidth, BoxHeight, Theme.NeutralDark.R, Theme.NeutralDark.G, Theme.NeutralDark.B, 0.97f);
        renderer.AddRect(boxX, boxY, BoxWidth, 4f, Theme.Tertiary.R, Theme.Tertiary.G, Theme.Tertiary.B, 1f);

        const float Pad = 24f;
        float x = boxX + Pad, y = boxY + Pad;
        renderer.AddText($"¡Quiere aprender {MoveCatalog.NameOf(_current.NewMoveId)}!", x, y, Theme.Tertiary.R, Theme.Tertiary.G, Theme.Tertiary.B, 1f);
        y += lineH * 1.4f;
        renderer.AddText("Pero ya conoce 4 movimientos. ¿A cuál reemplazar?", x, y, Theme.White.R, Theme.White.G, Theme.White.B, 0.9f);
        y += lineH * 1.6f;

        for (int i = 0; i < 4 && i < _current.CurrentMoveIds.Length; i++)
        {
            bool sel = i == _selection;
            if (sel) renderer.AddRect(x - 6f, y - 2f, BoxWidth - Pad * 2f - 12f, lineH * 1.2f, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 0.3f);
            (float r, float g, float b) = sel ? Theme.Primary : Theme.NeutralLight;
            renderer.AddText($"{(sel ? "> " : "  ")}{MoveCatalog.NameOf(_current.CurrentMoveIds[i])}", x, y, r, g, b, 1f);
            y += lineH * 1.2f;
        }

        bool declineSelected = _selection == 4;
        if (declineSelected) renderer.AddRect(x - 6f, y - 2f, BoxWidth - Pad * 2f - 12f, lineH * 1.2f, Theme.Secondary.R, Theme.Secondary.G, Theme.Secondary.B, 0.3f);
        {
            (float r, float g, float b) = declineSelected ? Theme.Primary : Theme.NeutralLight;
            renderer.AddText($"{(declineSelected ? "> " : "  ")}No aprender", x, y, r, g, b, 1f);
        }
        y += lineH * 1.6f;
        renderer.AddText("Arriba/Abajo: elegir   Enter: confirmar   Escape: no aprender", x, y, 0.6f, 0.6f, 0.6f, 0.8f);
    }
}
