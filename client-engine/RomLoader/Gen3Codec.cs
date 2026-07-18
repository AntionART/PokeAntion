namespace RomLoader;

/// <summary>
/// Algoritmo de encriptación de datos de guardado de Pokémon Generación 3 (Ruby/Sapphire/
/// Emerald/FireRed/LeafGreen) — dinero y estructuras de Pokémon van cifrados en la RAM del
/// juego, no en texto plano. Este es el mismo algoritmo documentado públicamente en el proyecto
/// de descompilación pokeemerald (no es específico de una ROM: es lógica de programa compilada
/// igual en cualquier build de Emerald, a diferencia de las DIRECCIONES de memoria, que sí son
/// específicas de cada ROM y se validan por separado en cada memory-map JSON). Su corrección se
/// valida empíricamente contra datos reales de una partida en vivo (ver find_torchic.js), no se
/// asume.
/// </summary>
public static class Gen3Codec
{
    public static uint DecryptMoney(uint rawValue, uint encryptionKey) => rawValue ^ encryptionKey;
    public static uint EncryptMoney(uint value, uint encryptionKey) => value ^ encryptionKey;

    /// <summary>Orden de las 4 substructuras (G=Growth, A=Attacks, E=EVs/Condition, M=Misc)
    /// dentro del bloque de 48 bytes cifrado de un Pokémon, indexado por personality % 24.
    /// Tabla estándar de pokeemerald (gBoxPokemonSubstructOrder equivalente).</summary>
    private static readonly int[][] SubstructOrder =
    [
        [0, 1, 2, 3], [0, 1, 3, 2], [0, 2, 1, 3], [0, 3, 1, 2], [0, 2, 3, 1], [0, 3, 2, 1],
        [1, 0, 2, 3], [1, 0, 3, 2], [2, 0, 1, 3], [3, 0, 1, 2], [2, 0, 3, 1], [3, 0, 2, 1],
        [1, 2, 0, 3], [1, 3, 0, 2], [2, 1, 0, 3], [3, 1, 0, 2], [2, 3, 0, 1], [3, 2, 0, 1],
        [1, 2, 3, 0], [1, 3, 2, 0], [2, 1, 3, 0], [3, 1, 2, 0], [2, 3, 1, 0], [3, 2, 1, 0],
    ];

    public const int BoxPokemonSize = 80;
    private const int DataSectionOffset = 0x20;
    private const int DataSectionSize = 48;

    public readonly struct DecryptedPokemon
    {
        public required uint Personality { get; init; }
        public required uint OtId { get; init; }
        public required ushort StoredChecksum { get; init; }
        public required ushort ComputedChecksum { get; init; }
        public bool ChecksumValid => StoredChecksum == ComputedChecksum;

        /// <summary>Las 4 substructuras ya des-permutadas, en orden canónico fijo: [0]=Growth
        /// (species u16@0, item u16@2, experience u32@4, ppBonuses u8@8, friendship u8@9),
        /// [1]=Attacks, [2]=EVs/Condition, [3]=Misc. 12 bytes cada una.</summary>
        public required byte[][] Substructs { get; init; }

        public ushort Species => (ushort)(Substructs[0][0] | (Substructs[0][1] << 8));
    }

    /// <summary>Descifra los 80 bytes de un BoxPokemon (personality..checksum..data cifrada).
    /// No valida el checksum por vos: revisá <see cref="DecryptedPokemon.ChecksumValid"/>.</summary>
    public static DecryptedPokemon DecryptBoxPokemon(ReadOnlySpan<byte> boxMon)
    {
        if (boxMon.Length < BoxPokemonSize)
            throw new ArgumentException($"Se esperaban {BoxPokemonSize} bytes, llegaron {boxMon.Length}.");

        uint personality = ReadU32(boxMon, 0);
        uint otId = ReadU32(boxMon, 4);
        ushort storedChecksum = ReadU16(boxMon, 0x1C);
        uint key = personality ^ otId;

        // Descifra las 12 palabras de 32 bits de la sección de datos con la clave.
        Span<byte> plain = stackalloc byte[DataSectionSize];
        for (int i = 0; i < DataSectionSize; i += 4)
        {
            uint word = ReadU32(boxMon, DataSectionOffset + i) ^ key;
            plain[i] = (byte)(word & 0xFF);
            plain[i + 1] = (byte)((word >> 8) & 0xFF);
            plain[i + 2] = (byte)((word >> 16) & 0xFF);
            plain[i + 3] = (byte)(word >> 24);
        }

        ushort computedChecksum = 0;
        for (int i = 0; i < DataSectionSize; i += 2)
            computedChecksum += (ushort)(plain[i] | (plain[i + 1] << 8));

        int[] order = SubstructOrder[personality % 24];
        var substructs = new byte[4][];
        for (int slot = 0; slot < 4; slot++)
        {
            int structIndex = order[slot]; // qué substructura (0=G,1=A,2=E,3=M) está en esta posición
            substructs[structIndex] = plain.Slice(slot * 12, 12).ToArray();
        }

        return new DecryptedPokemon
        {
            Personality = personality,
            OtId = otId,
            StoredChecksum = storedChecksum,
            ComputedChecksum = computedChecksum,
            Substructs = substructs,
        };
    }

    /// <summary>Arma los 80 bytes de un BoxPokemon cifrado listo para escribir en RAM, a partir
    /// de las 4 substructuras en orden canónico (mismo layout que <see cref="DecryptedPokemon.Substructs"/>).</summary>
    public static byte[] EncryptBoxPokemon(uint personality, uint otId, string nickname, string otName, byte[][] substructsCanonical)
    {
        if (substructsCanonical.Length != 4 || substructsCanonical.Any(s => s.Length != 12))
            throw new ArgumentException("Se esperan 4 substructuras de 12 bytes cada una.");

        var result = new byte[BoxPokemonSize];
        WriteU32(result, 0, personality);
        WriteU32(result, 4, otId);
        WriteFixedString(result, 0x08, nickname, 10);
        result[0x12] = 2; // idioma: 2 = inglés (mismo valor que usa el juego para nombres en inglés)
        WriteFixedString(result, 0x14, otName, 7);
        result[0x1B] = 0; // markings

        uint key = personality ^ otId;
        int[] order = SubstructOrder[personality % 24];

        Span<byte> plain = stackalloc byte[DataSectionSize];
        for (int slot = 0; slot < 4; slot++)
        {
            int structIndex = order[slot];
            substructsCanonical[structIndex].CopyTo(plain.Slice(slot * 12, 12));
        }

        ushort checksum = 0;
        for (int i = 0; i < DataSectionSize; i += 2)
            checksum += (ushort)(plain[i] | (plain[i + 1] << 8));
        WriteU16(result, 0x1C, checksum);

        for (int i = 0; i < DataSectionSize; i += 4)
        {
            uint word = (uint)(plain[i] | (plain[i + 1] << 8) | (plain[i + 2] << 16) | (plain[i + 3] << 24));
            WriteU32(result, DataSectionOffset + i, word ^ key);
        }

        return result;
    }

    /// <summary>Arma la substructura Growth (species, item, experience, ppBonuses, friendship)
    /// en su formato canónico de 12 bytes, lista para pasar a <see cref="EncryptBoxPokemon"/>.</summary>
    public static byte[] BuildGrowthSubstruct(ushort species, ushort heldItem, uint experience, byte friendship)
    {
        var s = new byte[12];
        s[0] = (byte)(species & 0xFF); s[1] = (byte)(species >> 8);
        s[2] = (byte)(heldItem & 0xFF); s[3] = (byte)(heldItem >> 8);
        s[4] = (byte)(experience & 0xFF); s[5] = (byte)((experience >> 8) & 0xFF);
        s[6] = (byte)((experience >> 16) & 0xFF); s[7] = (byte)(experience >> 24);
        s[8] = 0; // ppBonuses
        s[9] = friendship;
        s[10] = 0; s[11] = 0; // unknown/padding
        return s;
    }

    private static uint ReadU32(ReadOnlySpan<byte> b, int off) =>
        (uint)(b[off] | (b[off + 1] << 8) | (b[off + 2] << 16) | (b[off + 3] << 24));

    private static ushort ReadU16(ReadOnlySpan<byte> b, int off) =>
        (ushort)(b[off] | (b[off + 1] << 8));

    private static void WriteU32(byte[] b, int off, uint v)
    {
        b[off] = (byte)(v & 0xFF); b[off + 1] = (byte)((v >> 8) & 0xFF);
        b[off + 2] = (byte)((v >> 16) & 0xFF); b[off + 3] = (byte)(v >> 24);
    }

    private static void WriteU16(byte[] b, int off, ushort v)
    {
        b[off] = (byte)(v & 0xFF); b[off + 1] = (byte)(v >> 8);
    }

    private static void WriteFixedString(byte[] b, int off, string ascii, int maxLen)
    {
        // Codificación simplificada: no es la tabla de caracteres real de Gen3 (que no es ASCII),
        // solo rellena con 0xFF (terminador) — suficiente para uso interno/no mostrado todavía.
        for (int i = 0; i < maxLen; i++) b[off + i] = 0xFF;
    }
}
