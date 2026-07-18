namespace ClientApp;

/// <summary>
/// Paleta predefinida de colores de personaje (ver server/internal/world/colors.go —
/// AllowedSpriteColors tiene los mismos nombres, el servidor solo valida el nombre, el valor
/// RGB real vive acá). El tinte se aplica MULTIPLICANDO cada canal del sprite ya decodificado
/// (ver Renderer.SpritePixelShaderSource) — no es un reemplazo de paleta real como haría el
/// juego original, así que los valores están ajustados para desplazar el matiz sin aplastar
/// del todo ningún canal (evitar (1,0,0) puro, que dejaría el sprite plano/sin sombreado).
/// </summary>
internal static class SpriteColors
{
    public static readonly (string Name, string Label, float R, float G, float B)[] Presets =
    [
        ("default", "Natural", 1.00f, 1.00f, 1.00f),
        ("red",     "Rojo",    1.60f, 0.55f, 0.55f),
        ("blue",    "Azul",    0.55f, 0.75f, 1.60f),
        ("green",   "Verde",   0.55f, 1.50f, 0.55f),
        ("yellow",  "Amarillo",1.50f, 1.50f, 0.40f),
        ("purple",  "Morado",  1.30f, 0.55f, 1.60f),
        ("orange",  "Naranja", 1.60f, 0.95f, 0.40f),
        ("pink",    "Rosa",    1.60f, 0.75f, 1.10f),
        ("cyan",    "Celeste", 0.50f, 1.50f, 1.50f),
    ];

    public static (float R, float G, float B) Rgb(string? name)
    {
        foreach (var p in Presets)
            if (p.Name == name) return (p.R, p.G, p.B);
        return (1f, 1f, 1f); // desconocido -> sin tinte, nunca romper el render por un nombre raro
    }

    public static string LabelOf(string? name)
    {
        foreach (var p in Presets)
            if (p.Name == name) return p.Label;
        return "Natural";
    }
}
