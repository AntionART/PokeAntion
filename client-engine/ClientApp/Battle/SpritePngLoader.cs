using System.Drawing;
using System.Drawing.Imaging;
using System.Runtime.InteropServices;

namespace ClientApp.Battle;

/// <summary>
/// Decodifica un sprite de Pokémon (PNG indexado 4bpp con paleta embebida, 64x64, ver
/// graphics/pokemon/&lt;especie&gt;/front.png|back.png en el checkout de pokeemerald-master)
/// a RGBA32 top-down. Los píxeles de fondo (índice de paleta 0 en el GBA) ya vienen con
/// alfa=0 en el PNG del decomp, así que no hace falta adivinar un color de chroma-key.
/// </summary>
internal static class SpritePngLoader
{
    /// <summary>
    /// El orden de canal de salida es R,G,B,A (no B,G,R,A como PixelsBgra de FontAtlas):
    /// Renderer sube texturas de sprite como DXGI R8G8B8A8_UNorm (ver
    /// Renderer.UploadSpriteTexture/EnsureUiTexture), a diferencia del atlas de fuente que
    /// usa B8G8R8A8_UNorm — GDI+ entrega Format32bppArgb en memoria como B,G,R,A, así que hay
    /// que permutar los canales acá, no solo copiarlos.
    /// </summary>
    public static (byte[] Rgba, int Width, int Height) Load(string pngPath)
    {
        using var original = new Bitmap(pngPath);
        using Bitmap argb = original.PixelFormat == PixelFormat.Format32bppArgb
            ? (Bitmap)original.Clone()
            : original.Clone(new Rectangle(0, 0, original.Width, original.Height), PixelFormat.Format32bppArgb);

        int width = argb.Width, height = argb.Height;
        byte[] rgba = new byte[width * height * 4];
        BitmapData data = argb.LockBits(new Rectangle(0, 0, width, height), ImageLockMode.ReadOnly, PixelFormat.Format32bppArgb);
        try
        {
            unsafe
            {
                byte* src = (byte*)data.Scan0;
                for (int y = 0; y < height; y++)
                {
                    byte* row = src + y * data.Stride;
                    int rowOut = y * width * 4;
                    for (int x = 0; x < width; x++)
                    {
                        byte b = row[x * 4 + 0], g = row[x * 4 + 1], r = row[x * 4 + 2], a = row[x * 4 + 3];
                        int o = rowOut + x * 4;
                        rgba[o + 0] = r;
                        rgba[o + 1] = g;
                        rgba[o + 2] = b;
                        rgba[o + 3] = a;
                    }
                }
            }
        }
        finally
        {
            argb.UnlockBits(data);
        }
        return (rgba, width, height);
    }
}
