namespace ClientApp.Battle;

/// <summary>
/// Mapea un species ID a su carpeta real dentro de graphics/pokemon/ del checkout local de
/// pokeemerald-master, para cualquiera de las ~386 especies — sale de PokedexCatalog
/// (data/pokemon/species.json), no de una tabla hardcodeada de los 3 iniciales como antes.
/// </summary>
internal static class SpriteCatalog
{
    public static string? FolderFor(int species)
    {
        var entry = PokedexCatalog.Species(species);
        return entry != null && entry.SpriteFolder.Length > 0 ? entry.SpriteFolder : null;
    }
}
