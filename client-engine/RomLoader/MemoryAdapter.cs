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
    [JsonPropertyName("save_block_pointers")] public SaveBlockPointersConfig? SaveBlockPointers { get; set; }

    public sealed class PlayerConfig
    {
        [JsonPropertyName("pos_x_address")] public string? PosXAddress { get; set; }
        [JsonPropertyName("pos_y_address")] public string? PosYAddress { get; set; }
        [JsonPropertyName("map_number_address")] public string? MapNumberAddress { get; set; }
        [JsonPropertyName("sprite")] public PlayerSpriteConfig? Sprite { get; set; }
    }

    /// <summary>
    /// SaveBlock1/SaveBlock2 (dinero, equipo, posición, flags de historia) NO viven en
    /// direcciones fijas — son punteros a memoria reservada dinámicamente que el juego puede
    /// realocar en cualquier momento (confirmado empíricamente 2026-07-21: cambió de dirección
    /// solo por abrir el menú Bolsa, sin reiniciar nada). Lo único estable es la RANURA en IWRAM
    /// donde vive el puntero en sí (una variable global) — hay que releerla en cada acceso,
    /// nunca cachear la dirección resuelta de SaveBlock1/2 entre frames. Los offsets de campos
    /// son relativos a la base de cada save block y salen del código fuente real de pokeemerald
    /// (ver gen3_save_pointers memory), no de adivinar.
    /// </summary>
    public sealed class SaveBlockPointersConfig
    {
        [JsonPropertyName("save_block1_pointer_address")] public string? SaveBlock1PointerAddress { get; set; }
        [JsonPropertyName("save_block2_pointer_address")] public string? SaveBlock2PointerAddress { get; set; }
        [JsonPropertyName("pos_offset")] public string? PosOffset { get; set; }
        [JsonPropertyName("map_num_offset")] public string? MapNumOffset { get; set; }
        [JsonPropertyName("player_party_offset")] public string? PlayerPartyOffset { get; set; }
        [JsonPropertyName("player_party_count_offset")] public string? PlayerPartyCountOffset { get; set; }
        [JsonPropertyName("money_offset")] public string? MoneyOffset { get; set; }
        [JsonPropertyName("flags_offset")] public string? FlagsOffset { get; set; }
        [JsonPropertyName("encryption_key_offset")] public string? EncryptionKeyOffset { get; set; }
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

    /// <summary>
    /// Dinero del jugador, ya desencriptado (SaveBlock1->money XOR SaveBlock2->encryptionKey).
    /// Devuelve null si el memory-map no define save_block_pointers/money_offset. SaveBlock1/2
    /// se re-resuelven en cada llamada (nunca cachear la dirección: el juego puede realocarlos
    /// en cualquier momento, ver gen3_save_pointers memory).
    /// </summary>
    int? GetMoney();

    /// <summary>
    /// Equipo actual del jugador (hasta 6), ya desencriptado con Gen3Codec. Devuelve null si el
    /// memory-map no define save_block_pointers/player_party_offset. Cada Pokémon incluye
    /// ChecksumValid — un checksum inválido indica que SaveBlock1 se está realocando en este
    /// preciso instante (leer de nuevo en el próximo frame en vez de confiar en el dato).
    /// </summary>
    IReadOnlyList<Gen3Codec.DecryptedPokemon>? GetParty();

    /// <summary>
    /// Escribe el dinero del jugador (encriptado con la encryptionKey vigente de SaveBlock2,
    /// releída en el momento — nunca la clave de una lectura anterior). No hace nada si el
    /// memory-map no define save_block_pointers completos.
    /// </summary>
    void SetMoney(int amount);

    /// <summary>
    /// Lee/escribe un flag de historia (ej. FLAG_SYS_CLOCK_SET) por su ID numérico de
    /// pokeemerald (include/constants/flags.h) — el adaptador calcula el byte/bit dentro del
    /// array de flags. No hace nada / devuelve null si el memory-map no define flags_offset.
    /// </summary>
    bool? GetFlag(int flagId);
    void SetFlag(int flagId, bool value);

    /// <summary>
    /// Escribe un Pokémon completo (encriptado) en un slot del equipo (0-5), incluyendo las
    /// estadísticas en claro (nivel, HP, ataque, etc. — no forman parte del bloque cifrado).
    /// Pensado para el "inicializador de partida nueva": darle al jugador su inicial elegido
    /// sin tener que jugar la intro. No hace nada si el memory-map no define
    /// save_block_pointers/player_party_offset.
    /// </summary>
    void SetPartyPokemon(int slot, Gen3Codec.NewPokemonSpec spec);
}

public sealed class GbaMemoryAdapter : IMemoryAdapter
{
    private readonly MemoryMapConfig _config;
    private readonly GbaMemoryBus _bus;

    public GbaMemoryAdapter(MgbaCore core, MemoryMapConfig config)
    {
        _config = config;
        _bus = new GbaMemoryBus(core);
    }

    public PlayerPosition GetPlayerPosition()
    {
        var sb = _config.SaveBlockPointers;
        if (sb?.SaveBlock1PointerAddress != null && sb.PosOffset != null)
        {
            uint baseAddr = ResolvePointer(sb.SaveBlock1PointerAddress);
            uint posOffset = ParseAddress(sb.PosOffset);
            ushort px = _bus.ReadU16(baseAddr + posOffset);
            ushort py = _bus.ReadU16(baseAddr + posOffset + 2);
            return new PlayerPosition(px, py);
        }

        if (_config.Player.PosXAddress is null || _config.Player.PosYAddress is null)
        {
            throw new InvalidOperationException(
                $"{_config.RomId}: player.pos_x_address/pos_y_address (o save_block_pointers.pos_offset) no están definidas en el memory-map.");
        }

        ushort x = _bus.ReadU16(ParseAddress(_config.Player.PosXAddress));
        ushort y = _bus.ReadU16(ParseAddress(_config.Player.PosYAddress));
        return new PlayerPosition(x, y);
    }

    public byte GetMapNumber()
    {
        var sb = _config.SaveBlockPointers;
        if (sb?.SaveBlock1PointerAddress != null && sb.MapNumOffset != null)
        {
            uint baseAddr = ResolvePointer(sb.SaveBlock1PointerAddress);
            return _bus.ReadU8(baseAddr + ParseAddress(sb.MapNumOffset));
        }

        if (_config.Player.MapNumberAddress is null)
        {
            throw new InvalidOperationException(
                $"{_config.RomId}: player.map_number_address (o save_block_pointers.map_num_offset) no está definida en el memory-map.");
        }

        return _bus.ReadU8(ParseAddress(_config.Player.MapNumberAddress));
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

    public int? GetMoney()
    {
        var sb = _config.SaveBlockPointers;
        if (sb?.SaveBlock1PointerAddress is null || sb.SaveBlock2PointerAddress is null ||
            sb.MoneyOffset is null || sb.EncryptionKeyOffset is null)
        {
            return null;
        }

        uint saveBlock1 = ResolvePointer(sb.SaveBlock1PointerAddress);
        uint saveBlock2 = ResolvePointer(sb.SaveBlock2PointerAddress);
        uint moneyRaw = _bus.ReadU32(saveBlock1 + ParseAddress(sb.MoneyOffset));
        uint key = _bus.ReadU32(saveBlock2 + ParseAddress(sb.EncryptionKeyOffset));
        return (int)Gen3Codec.DecryptMoney(moneyRaw, key);
    }

    public IReadOnlyList<Gen3Codec.DecryptedPokemon>? GetParty()
    {
        var sb = _config.SaveBlockPointers;
        if (sb?.SaveBlock1PointerAddress is null || sb.PlayerPartyOffset is null || sb.PlayerPartyCountOffset is null)
            return null;

        uint saveBlock1 = ResolvePointer(sb.SaveBlock1PointerAddress);
        byte count = _bus.ReadU8(saveBlock1 + ParseAddress(sb.PlayerPartyCountOffset));
        uint partyBase = saveBlock1 + ParseAddress(sb.PlayerPartyOffset);

        var result = new List<Gen3Codec.DecryptedPokemon>();
        for (int i = 0; i < Math.Min((int)count, 6); i++)
        {
            byte[] slot = _bus.ReadBytes(partyBase + (uint)(i * PartySlotSize), Gen3Codec.BoxPokemonSize);
            result.Add(Gen3Codec.DecryptBoxPokemon(slot));
        }
        return result;
    }

    public void SetMoney(int amount)
    {
        var sb = _config.SaveBlockPointers;
        if (sb?.SaveBlock1PointerAddress is null || sb.SaveBlock2PointerAddress is null ||
            sb.MoneyOffset is null || sb.EncryptionKeyOffset is null)
        {
            return;
        }

        uint saveBlock1 = ResolvePointer(sb.SaveBlock1PointerAddress);
        uint saveBlock2 = ResolvePointer(sb.SaveBlock2PointerAddress);
        uint key = _bus.ReadU32(saveBlock2 + ParseAddress(sb.EncryptionKeyOffset));
        _bus.WriteU32(saveBlock1 + ParseAddress(sb.MoneyOffset), Gen3Codec.EncryptMoney((uint)amount, key));
    }

    public bool? GetFlag(int flagId)
    {
        var sb = _config.SaveBlockPointers;
        if (sb?.SaveBlock1PointerAddress is null || sb.FlagsOffset is null) return null;

        uint saveBlock1 = ResolvePointer(sb.SaveBlock1PointerAddress);
        var (byteOffset, bit) = FlagByteAndBit(flagId);
        byte flagByte = _bus.ReadU8(saveBlock1 + ParseAddress(sb.FlagsOffset) + (uint)byteOffset);
        return (flagByte & (1 << bit)) != 0;
    }

    public void SetFlag(int flagId, bool value)
    {
        var sb = _config.SaveBlockPointers;
        if (sb?.SaveBlock1PointerAddress is null || sb.FlagsOffset is null) return;

        uint saveBlock1 = ResolvePointer(sb.SaveBlock1PointerAddress);
        var (byteOffset, bit) = FlagByteAndBit(flagId);
        uint flagAddr = saveBlock1 + ParseAddress(sb.FlagsOffset) + (uint)byteOffset;
        byte flagByte = _bus.ReadU8(flagAddr);
        flagByte = value ? (byte)(flagByte | (1 << bit)) : (byte)(flagByte & ~(1 << bit));
        _bus.WriteU8(flagAddr, flagByte);
    }

    public void SetPartyPokemon(int slot, Gen3Codec.NewPokemonSpec spec)
    {
        if (slot is < 0 or > 5) throw new ArgumentOutOfRangeException(nameof(slot), "El equipo tiene 6 slots (0-5).");

        var sb = _config.SaveBlockPointers;
        if (sb?.SaveBlock1PointerAddress is null || sb.PlayerPartyOffset is null || sb.PlayerPartyCountOffset is null)
            return;

        uint saveBlock1 = ResolvePointer(sb.SaveBlock1PointerAddress);
        uint partyBase = saveBlock1 + ParseAddress(sb.PlayerPartyOffset);
        _bus.WriteBytes(partyBase + (uint)(slot * PartySlotSize), Gen3Codec.BuildFullPartySlot(spec));

        byte count = _bus.ReadU8(saveBlock1 + ParseAddress(sb.PlayerPartyCountOffset));
        if (slot >= count)
            _bus.WriteU8(saveBlock1 + ParseAddress(sb.PlayerPartyCountOffset), (byte)(slot + 1));
    }

    /// <summary>Lee el puntero VIVO desde su ranura fija de IWRAM — nunca cachear el resultado
    /// entre llamadas, SaveBlock1/2 pueden realocarse en cualquier momento (ver clase de
    /// SaveBlockPointersConfig).</summary>
    private uint ResolvePointer(string pointerHexAddress) =>
        _bus.ReadU32(ParseAddress(pointerHexAddress));

    /// <summary>struct Pokemon completo (80 bytes BoxPokemon cifrado + 20 bytes de
    /// estadísticas en claro), tamaño fijo del hardware/juego — no depende de la ROM.</summary>
    private const int PartySlotSize = 100;

    private static (int ByteOffset, int Bit) FlagByteAndBit(int flagId) => (flagId / 8, flagId % 8);

    private static uint ParseAddress(string hex) =>
        Convert.ToUInt32(hex, 16);
}
