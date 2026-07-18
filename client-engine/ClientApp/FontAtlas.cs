using System.Drawing;
using System.Drawing.Imaging;
using System.Drawing.Text;
using System.Runtime.InteropServices;

namespace ClientApp;

/// <summary>Rectángulo UV (0..1) y ancho de avance de un glifo dentro del atlas.</summary>
internal readonly record struct Glyph(float U0, float V0, float U1, float V1, float AdvancePx, float WidthPx, float HeightPx);

/// <summary>
/// Rasteriza un juego de caracteres (ASCII imprimible + acentos/ñ/¿/¡ del español) a un
/// único bitmap con GDI+ (System.Drawing, no captura de pantalla — eso es lo único que
/// falla en esta sesión RDP) y lo deja listo para subir como textura. El layout es una
/// grilla simple: todas las celdas del mismo tamaño (el máximo bounding box medido).
/// </summary>
internal sealed class FontAtlas
{
    // Todo lo imprimible que puede aparecer en chat/nombres en español.
    private const string Charset =
        " !\"#$%&'()*+,-./0123456789:;<=>?@" +
        "ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`" +
        "abcdefghijklmnopqrstuvwxyz{|}~" +
        "ÁÉÍÓÚÑáéíóúñ¿¡Üü";

    public byte[] PixelsBgra { get; }
    public int Width { get; }
    public int Height { get; }
    public float LineHeightPx { get; }

    private readonly Dictionary<char, Glyph> _glyphs = new();

    public FontAtlas(string fontFamily = "Consolas", float sizePt = 16f)
    {
        using var font = new Font(fontFamily, sizePt, FontStyle.Regular, GraphicsUnit.Pixel);

        // Medir todas las celdas primero con un Graphics descartable, para saber el tamaño del atlas.
        // Consolas es monoespaciada: en vez de confiar en MeasureString por carácter (GDI+ con
        // StringFormat.GenericTypographic mide mal el espacio " " — recorta el ancho a casi 0,
        // lo que pegaba todas las palabras entre sí), se mide UNA vez con una letra de referencia
        // y ese mismo ancho de avance se usa para TODOS los glifos, espacio incluido.
        float monospaceAdvance;
        SizeF maxSize;
        using (var probe = new Bitmap(1, 1))
        using (var g = Graphics.FromImage(probe))
        {
            g.TextRenderingHint = TextRenderingHint.AntiAliasGridFit;
            monospaceAdvance = g.MeasureString("M", font, PointF.Empty, StringFormat.GenericTypographic).Width;
            float maxW = 0, maxH = 0;
            foreach (char c in Charset)
            {
                SizeF sz = g.MeasureString(c.ToString(), font, PointF.Empty, StringFormat.GenericTypographic);
                maxW = Math.Max(maxW, sz.Width);
                maxH = Math.Max(maxH, sz.Height);
            }
            maxSize = new SizeF(MathF.Ceiling(maxW) + 2, MathF.Ceiling(maxH) + 2);
        }

        int cols = (int)Math.Ceiling(Math.Sqrt(Charset.Length));
        int rows = (int)Math.Ceiling(Charset.Length / (double)cols);
        int cellW = (int)maxSize.Width;
        int cellH = (int)maxSize.Height;
        Width = cols * cellW;
        Height = rows * cellH;
        LineHeightPx = cellH;

        using var bmp = new Bitmap(Width, Height, PixelFormat.Format32bppArgb);
        using (var g = Graphics.FromImage(bmp))
        {
            g.Clear(Color.Transparent);
            g.TextRenderingHint = TextRenderingHint.AntiAliasGridFit;
            using var brush = new SolidBrush(Color.White);
            for (int i = 0; i < Charset.Length; i++)
            {
                char c = Charset[i];
                int col = i % cols, row = i / cols;
                float x = col * cellW, y = row * cellH;
                g.DrawString(c.ToString(), font, brush, new PointF(x + 1, y + 1), StringFormat.GenericTypographic);
                _glyphs[c] = new Glyph(
                    x / Width, y / Height, (x + cellW) / Width, (y + cellH) / Height,
                    monospaceAdvance, cellW, cellH);
            }
        }

        PixelsBgra = new byte[Width * Height * 4];
        BitmapData data = bmp.LockBits(new Rectangle(0, 0, Width, Height), ImageLockMode.ReadOnly, PixelFormat.Format32bppArgb);
        try
        {
            unsafe
            {
                byte* src = (byte*)data.Scan0;
                for (int y = 0; y < Height; y++)
                    Marshal.Copy((IntPtr)(src + y * data.Stride), PixelsBgra, y * Width * 4, Width * 4);
            }
        }
        finally
        {
            bmp.UnlockBits(data);
        }
    }

    public Glyph GetGlyph(char c) => _glyphs.TryGetValue(c, out var g) ? g : _glyphs[' '];
}
