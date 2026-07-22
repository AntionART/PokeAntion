namespace ClientApp.Battle;

/// <summary>
/// Mapea un species ID (mismo número que server/internal/pokemon.SpeciesTreecko/etc. y
/// RomLoader.StarterCatalog) al nombre de carpeta real dentro de graphics/pokemon/ del
/// checkout local de pokeemerald-master — ese nombre no es derivable del ID sin la tabla
/// completa de nombres de especie, así que se hardcodea, igual que ya se hace en
/// server/internal/pokemon/starters.go para los mismos 3 iniciales.
/// </summary>
internal static class SpriteCatalog
{
    private static readonly Dictionary<int, string> FolderBySpecies = new()
    {
        [277] = "treecko",
        [280] = "torchic",
        [283] = "mudkip",
    };

    public static string? FolderFor(int species) => FolderBySpecies.TryGetValue(species, out var folder) ? folder : null;
}
