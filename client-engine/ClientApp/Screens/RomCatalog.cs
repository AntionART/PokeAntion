using RomLoader;

namespace ClientApp.Screens;

/// <summary>Una ROM jugable: ya validada contra un memory-map y presente en este equipo.</summary>
public sealed record RomCatalogEntry(string RomId, string DisplayName, string RomPath, string MemoryMapPath);

/// <summary>
/// Arma la lista de ROMs que el motor puede ofrecer en la pantalla de selección, leyendo
/// memory-maps/*.json (cada uno declara su rom_id, display_name y rom_path — ver ese
/// directorio). Solo entran al catálogo las entradas cuyo archivo .gba realmente existe en
/// este equipo: agregar soporte a una ROM nueva es tan simple como poner el .gba en su lugar
/// y su memory-map al lado, sin tocar código del cliente.
/// </summary>
public static class RomCatalog
{
    public static List<RomCatalogEntry> Discover(string memoryMapsDir, string repoRoot)
    {
        var result = new List<RomCatalogEntry>();
        if (!Directory.Exists(memoryMapsDir)) return result;

        foreach (string mapPath in Directory.GetFiles(memoryMapsDir, "*.json"))
        {
            MemoryMapConfig config;
            try
            {
                config = MemoryMapConfig.LoadFromFile(mapPath);
            }
            catch
            {
                continue; // memory-map corrupto/no parseable: se ignora, no tumba el catálogo entero
            }

            if (string.IsNullOrWhiteSpace(config.RomPath)) continue;

            string romPath = Path.GetFullPath(Path.Combine(repoRoot, config.RomPath));
            if (!File.Exists(romPath)) continue; // ROM no presente en este equipo: no se ofrece

            result.Add(new RomCatalogEntry(
                config.RomId,
                string.IsNullOrWhiteSpace(config.DisplayName) ? config.RomId : config.DisplayName,
                romPath,
                mapPath));
        }

        return result;
    }
}
