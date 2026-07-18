namespace LibretroCore;

/// <summary>Una entrada de OAM (Object Attribute Memory) ya decodificada a campos legibles.
/// Ver GBATek "LCD OBJ - OAM Attributes" para el formato crudo de 6 bytes por entrada.</summary>
public readonly record struct OamEntry(
    int Index, int X, int Y, int WidthPx, int HeightPx,
    int TileIndex, int PaletteIndex, bool HFlip, bool VFlip, bool Visible, int Priority);

/// <summary>
/// Extrae sprites directamente del hardware gráfico del GBA (OAM + VRAM de tiles + paleta de
/// objetos), no de direcciones específicas de una ROM. Esto es estándar de hardware — igual en
/// CUALQUIER juego/revisión de GBA — a diferencia de las direcciones de EWRAM del save block,
/// que sí son específicas de cada ROM y viven en memory-maps/*.json.
///
/// Convención de color de GBA: en modo 16 colores/paleta, el índice de color 0 de cada paleta
/// de OBJ es transparente (nunca se dibuja) — se traduce directo a alfa=0 en el RGBA de salida,
/// dando un recorte limpio del sprite sin tener que adivinar un color de fondo a descartar.
/// </summary>
public sealed class SpriteExtractor
{
    private const uint OamBase = 0x07000000;
    private const uint ObjPaletteBase = 0x05000200;
    private const uint ObjTileBase = 0x06010000;
    private const uint DispCntAddress = 0x04000000;

    private readonly GbaMemoryBus _bus;

    public SpriteExtractor(GbaMemoryBus bus) => _bus = bus;

    // [size, shape] -> (ancho, alto) en píxeles. shape: 0=Square 1=Horizontal 2=Vertical (3=inválido).
    private static readonly (int W, int H)[,] SizeShapeTable =
    {
        { (8, 8), (16, 8), (8, 16), (0, 0) },
        { (16, 16), (32, 8), (8, 32), (0, 0) },
        { (32, 32), (32, 16), (16, 32), (0, 0) },
        { (64, 64), (64, 32), (32, 64), (0, 0) },
    };

    /// <summary>true si REG_DISPCNT tiene el bit de mapeo de OBJ en 1D (bit 6). Los juegos de
    /// Pokémon Gen 3 usan 1D; si algún día esto da false hay que decodificar tiles distinto
    /// (mapeo 2D, filas fijas de 32 tiles en VRAM) — se deja el chequeo explícito en vez de
    /// asumir, para no fallar en silencio con una ROM/juego distinto.</summary>
    public bool Is1DObjMapping => (_bus.ReadU16(DispCntAddress) & 0x0040) != 0;

    public IReadOnlyList<OamEntry> ReadOam()
    {
        var list = new List<OamEntry>(128);
        for (int i = 0; i < 128; i++)
        {
            uint entryAddr = OamBase + (uint)(i * 8);
            ushort attr0 = _bus.ReadU16(entryAddr);
            ushort attr1 = _bus.ReadU16(entryAddr + 2);
            ushort attr2 = _bus.ReadU16(entryAddr + 4);

            bool rotScale = (attr0 & 0x0100) != 0;
            bool disabledFlag = !rotScale && (attr0 & 0x0200) != 0;
            int objMode = (attr0 >> 10) & 0x3; // 2 = "OBJ window" (máscara, no un sprite visible)
            int shape = (attr0 >> 14) & 0x3;

            int yRaw = attr0 & 0xFF;
            int y = yRaw >= 160 ? yRaw - 256 : yRaw; // coordenada con signo (wraparound de pantalla)

            int xRaw = attr1 & 0x1FF;
            int x = xRaw >= 240 ? xRaw - 512 : xRaw;
            bool hFlip = !rotScale && (attr1 & 0x1000) != 0;
            bool vFlip = !rotScale && (attr1 & 0x2000) != 0;
            int size = (attr1 >> 14) & 0x3;

            int tileIndex = attr2 & 0x3FF;
            int priority = (attr2 >> 10) & 0x3;
            int paletteIndex = (attr2 >> 12) & 0xF;

            (int w, int h) = shape == 3 ? (0, 0) : SizeShapeTable[size, shape];
            bool visible = !disabledFlag && objMode != 2 && w > 0;

            list.Add(new OamEntry(i, x, y, w, h, tileIndex, paletteIndex, hFlip, vFlip, visible, priority));
        }
        return list;
    }

    /// <summary>Paleta de un OBJ (16 colores, RGBA8, color 0 = transparente).</summary>
    public byte[] DecodePalette(int paletteIndex)
    {
        byte[] raw = _bus.ReadBytes(ObjPaletteBase + (uint)(paletteIndex * 32), 32);
        var rgba = new byte[16 * 4];
        for (int c = 0; c < 16; c++)
        {
            ushort v = (ushort)(raw[c * 2] | (raw[c * 2 + 1] << 8));
            int r5 = v & 0x1F, g5 = (v >> 5) & 0x1F, b5 = (v >> 10) & 0x1F;
            rgba[c * 4 + 0] = (byte)((r5 << 3) | (r5 >> 2));
            rgba[c * 4 + 1] = (byte)((g5 << 3) | (g5 >> 2));
            rgba[c * 4 + 2] = (byte)((b5 << 3) | (b5 >> 2));
            rgba[c * 4 + 3] = (byte)(c == 0 ? 0 : 255);
        }
        return rgba;
    }

    /// <summary>Decodifica un sprite 4bpp (16 colores) a RGBA8 top-down, asumiendo mapeo 1D de
    /// tiles (ver Is1DObjMapping). widthPx/heightPx deben ser múltiplos de 8.</summary>
    public byte[] DecodeSprite(int tileIndexBase, int widthPx, int heightPx, int paletteIndex, bool hFlip, bool vFlip)
    {
        byte[] palette = DecodePalette(paletteIndex);
        int tilesWide = widthPx / 8, tilesHigh = heightPx / 8;
        var outPixels = new byte[widthPx * heightPx * 4];

        for (int ty = 0; ty < tilesHigh; ty++)
        {
            for (int tx = 0; tx < tilesWide; tx++)
            {
                int tileNum = tileIndexBase + ty * tilesWide + tx; // mapeo 1D: tiles en orden raster
                byte[] tile = _bus.ReadBytes(ObjTileBase + (uint)(tileNum * 32), 32);
                for (int py = 0; py < 8; py++)
                {
                    for (int px = 0; px < 8; px++)
                    {
                        byte b = tile[py * 4 + px / 2];
                        int colorIdx = (px % 2 == 0) ? (b & 0xF) : (b >> 4);

                        int destX = tx * 8 + px;
                        int destY = ty * 8 + py;
                        if (hFlip) destX = widthPx - 1 - destX;
                        if (vFlip) destY = heightPx - 1 - destY;

                        int destOff = (destY * widthPx + destX) * 4;
                        outPixels[destOff + 0] = palette[colorIdx * 4 + 0];
                        outPixels[destOff + 1] = palette[colorIdx * 4 + 1];
                        outPixels[destOff + 2] = palette[colorIdx * 4 + 2];
                        outPixels[destOff + 3] = palette[colorIdx * 4 + 3];
                    }
                }
            }
        }
        return outPixels;
    }
}
