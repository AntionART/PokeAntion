namespace ClientApp.Battle;

/// <summary>
/// Punto de entrada único para cargar sprites de batalla: combina SpriteCatalog (qué carpeta)
/// + SpritePngLoader (cómo decodificarla). Devuelve null si la especie no está en el catálogo
/// (species no soportado hoy) o si el checkout de pokeemerald-master no está en spritesRootDir
/// (ej. otra máquina sin el .zip descomprimido) — el llamador decide el fallback (no dibujar
/// nada, o un cuadrado de color como los marcadores de overworld).
/// </summary>
internal static class BattleSpriteAssets
{
    public static (byte[] Rgba, int Width, int Height)? LoadFront(string spritesRootDir, int species) =>
        Load(spritesRootDir, species, "front.png");

    public static (byte[] Rgba, int Width, int Height)? LoadBack(string spritesRootDir, int species) =>
        Load(spritesRootDir, species, "back.png");

    private static (byte[], int, int)? Load(string spritesRootDir, int species, string fileName)
    {
        string? folder = SpriteCatalog.FolderFor(species);
        if (folder == null) return null;
        string path = Path.Combine(spritesRootDir, folder, fileName);
        if (!File.Exists(path)) return null;
        return SpritePngLoader.Load(path);
    }
}
