namespace ClientApp;

/// <summary>
/// Paleta de colores de la interfaz (pedida por el usuario, referencia visual "PokeArt":
/// cyan/magenta/dorado sobre negro, estilo cyberpunk-trainer). Valores en 0..1 (formato que
/// espera Renderer.AddText/AddRect), no en 0..255 — convertidos una sola vez acá para no
/// repetir la división por 255 en cada call site.
/// </summary>
internal static class Theme
{
    public static readonly (float R, float G, float B) Primary = FromHex(0x00, 0xF9, 0xFF);   // cian
    public static readonly (float R, float G, float B) Secondary = FromHex(0xFF, 0x00, 0x7A);  // magenta
    public static readonly (float R, float G, float B) Tertiary = FromHex(0xFF, 0xCB, 0x05);   // dorado
    public static readonly (float R, float G, float B) NeutralLight = FromHex(0xBA, 0xBA, 0xBB);
    public static readonly (float R, float G, float B) NeutralDark = FromHex(0x1A, 0x1A, 0x1C);
    public static readonly (float R, float G, float B) Background = FromHex(0x0D, 0x0D, 0x0F);
    public static readonly (float R, float G, float B) White = (1f, 1f, 1f);

    private static (float, float, float) FromHex(byte r, byte g, byte b) => (r / 255f, g / 255f, b / 255f);
}
