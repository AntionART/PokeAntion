using System.Text.Json.Serialization;
using LibretroCore;

namespace RomLoader;

public readonly record struct PlayerPosition(int X, int Y);

/// <summary>
/// Cómo identificar, entre todas las entradas de OAM visibles, cuál es el sprite del PROPIO
/// jugador (no un NPC) — esto NO es hardware genérico de GBA, es una convención de esta ROM en
/// particular (ej. en pokeemerald el jugador siempre usa PALSLOT_PLAYER, un índice de paleta
/// fijo, y un tamaño de sprite fijo). Otra ROM podría usar otro tamaño, otro índice de paleta,
/// o incluso otra posición de cámara esperada.
/// </summary>
public sealed class PlayerSpriteConfig
{
    [JsonPropertyName("width_px")] public int WidthPx { get; set; }
    [JsonPropertyName("height_px")] public int HeightPx { get; set; }
    [JsonPropertyName("palette_index")] public int PaletteIndex { get; set; }
    /// <summary>Punto de pantalla (en píxeles GBA, 240x160) donde suele estar el sprite del
    /// jugador cuando la cámara lo sigue — no es exactamente el centro geométrico de la
    /// pantalla (120,80), depende de cómo esa ROM en particular posiciona la cámara/UI. Se usa
    /// solo como desempate cuando hay más de un candidato que matchea tamaño+paleta (ej. varios
    /// NPCs con el mismo sprite base).</summary>
    [JsonPropertyName("expected_center_x")] public int ExpectedCenterX { get; set; } = 120;
    [JsonPropertyName("expected_center_y")] public int ExpectedCenterY { get; set; } = 80;
}

/// <summary>
/// Modelo tipado de un archivo memory-maps/*.json (ver ese directorio para el esquema y las
/// notas de validación de cada campo). Deliberadamente parcial: solo tipa los campos que el
/// adaptador sabe leer hoy (posición del jugador); el resto del JSON (save_block, notes, etc.)
/// se ignora al deserializar en vez de fallar.
/// </summary>
public sealed class MemoryMapConfig
{
    [JsonPropertyName("rom_id")] public string RomId { get; set; } = "";
    [JsonPropertyName("display_name")] public string DisplayName { get; set; } = "";
    /// <summary>Ruta al archivo .gba, relativa a la raíz del repo. Si falta o el archivo no
    /// existe en este equipo, RomCatalog.Discover (ClientApp/Screens) omite esta entrada del
    /// catálogo — así el selector de ROM solo ofrece lo que realmente se puede jugar acá.</summary>
    [JsonPropertyName("rom_path")] public string? RomPath { get; set; }
    [JsonPropertyName("player")] public PlayerConfig Player { get; set; } = new();

    public sealed class PlayerConfig
    {
        [JsonPropertyName("pos_x_address")] public string? PosXAddress { get; set; }
        [JsonPropertyName("pos_y_address")] public string? PosYAddress { get; set; }
        [JsonPropertyName("map_number_address")] public string? MapNumberAddress { get; set; }
        [JsonPropertyName("sprite")] public PlayerSpriteConfig? Sprite { get; set; }
    }

    public static MemoryMapConfig LoadFromFile(string path)
    {
        string json = File.ReadAllText(path);
        return System.Text.Json.JsonSerializer.Deserialize<MemoryMapConfig>(json)
            ?? throw new InvalidDataException($"No se pudo parsear el memory-map: {path}");
    }
}

/// <summary>
/// Traduce las direcciones de un memory-maps/*.json a lecturas concretas sobre el EWRAM que
/// expone MgbaCore. El resto del cliente debería hablar SOLO con esta interfaz — nunca
/// hardcodear una dirección de memoria fuera de acá (así es intercambiable entre ROMs, ver
/// Fase D.4/H del roadmap).
/// </summary>
public interface IMemoryAdapter
{
    /// <summary>
    /// Posición del jugador, en coordenadas de tile (no píxel).
    /// Lanza InvalidOperationException si el memory-map no define las direcciones, o si el
    /// core todavía no expone EWRAM (ROM no cargada / core no inicializado).
    /// </summary>
    PlayerPosition GetPlayerPosition();

    /// <summary>
    /// Identificador numérico crudo del mapa/edificio actual (un solo byte, validado
    /// empíricamente el 2026-07-16: estable en el mapa exterior y en cada interior distinto,
    /// reproducible en visitas repetidas — ver memory-maps/emerald_es.json player._map_note).
    /// NO es el mapGroup/mapNum "oficial" de pokeemerald, solo un byte que cambia de forma
    /// confiable en cada transición de mapa real — alcanza para detectar "¿estoy en el mismo
    /// mapa que este otro jugador?" por igualdad, aunque no alcance para nombrarlo.
    /// Lanza InvalidOperationException si el memory-map no define la dirección o si el core
    /// todavía no expone EWRAM.
    /// </summary>
    byte GetMapNumber();

    /// <summary>
    /// De todas las entradas de OAM visibles, encuentra cuál es el sprite del PROPIO jugador
    /// (no un NPC), según la convención declarada en player.sprite del memory-map (ver
    /// PlayerSpriteConfig). Devuelve null si no hay ningún candidato visible todavía (ej.
    /// pantalla de carga/transición) o si el memory-map no define player.sprite.
    /// </summary>
    OamEntry? FindPlayerSprite(IReadOnlyList<OamEntry> oamEntries);
}

public sealed class GbaMemoryAdapter : IMemoryAdapter
{
    private readonly MgbaCore _core;
    private readonly MemoryMapConfig _config;

    public GbaMemoryAdapter(MgbaCore core, MemoryMapConfig config)
    {
        _core = core;
        _config = config;
    }

    public PlayerPosition GetPlayerPosition()
    {
        if (_config.Player.PosXAddress is null || _config.Player.PosYAddress is null)
        {
            throw new InvalidOperationException(
                $"{_config.RomId}: player.pos_x_address/pos_y_address no están definidas en el memory-map.");
        }

        ushort x = ReadU16(ParseAddress(_config.Player.PosXAddress));
        ushort y = ReadU16(ParseAddress(_config.Player.PosYAddress));
        return new PlayerPosition(x, y);
    }

    public byte GetMapNumber()
    {
        if (_config.Player.MapNumberAddress is null)
        {
            throw new InvalidOperationException(
                $"{_config.RomId}: player.map_number_address no está definida en el memory-map.");
        }

        return ReadU8(ParseAddress(_config.Player.MapNumberAddress));
    }

    public OamEntry? FindPlayerSprite(IReadOnlyList<OamEntry> oamEntries)
    {
        PlayerSpriteConfig? cfg = _config.Player.Sprite;
        if (cfg is null) return null;

        OamEntry? best = null;
        int bestDist = int.MaxValue;
        foreach (var e in oamEntries)
        {
            if (!e.Visible || e.WidthPx != cfg.WidthPx || e.HeightPx != cfg.HeightPx || e.PaletteIndex != cfg.PaletteIndex)
                continue;
            int dist = Math.Abs(e.X - cfg.ExpectedCenterX) + Math.Abs(e.Y - cfg.ExpectedCenterY);
            if (dist < bestDist)
            {
                bestDist = dist;
                best = e;
            }
        }
        return best;
    }

    private static uint ParseAddress(string hex) =>
        Convert.ToUInt32(hex, 16);

    private unsafe ushort ReadU16(uint gbaAddress)
    {
        MemoryRegion ewram = _core.FindEwram()
            ?? throw new InvalidOperationException("EWRAM todavía no está disponible (¿se cargó la ROM?).");

        if (gbaAddress < ewram.Start || gbaAddress + 1 >= ewram.Start + ewram.Len)
        {
            throw new ArgumentOutOfRangeException(nameof(gbaAddress),
                $"0x{gbaAddress:X8} está fuera del rango de EWRAM (0x{ewram.Start:X8}-0x{ewram.Start + ewram.Len:X8}).");
        }

        nuint offset = gbaAddress - ewram.Start;
        byte* p = (byte*)ewram.Ptr + offset;
        return (ushort)(p[0] | (p[1] << 8));
    }

    private unsafe byte ReadU8(uint gbaAddress)
    {
        MemoryRegion ewram = _core.FindEwram()
            ?? throw new InvalidOperationException("EWRAM todavía no está disponible (¿se cargó la ROM?).");

        if (gbaAddress < ewram.Start || gbaAddress >= ewram.Start + ewram.Len)
        {
            throw new ArgumentOutOfRangeException(nameof(gbaAddress),
                $"0x{gbaAddress:X8} está fuera del rango de EWRAM (0x{ewram.Start:X8}-0x{ewram.Start + ewram.Len:X8}).");
        }

        nuint offset = gbaAddress - ewram.Start;
        byte* p = (byte*)ewram.Ptr + offset;
        return p[0];
    }
}
