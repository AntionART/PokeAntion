using System.Text.Json;

namespace ClientApp;

/// <summary>Forma exacta de una entrada de data/pokemon/maps.json (generado por
/// server/cmd/gendata desde pokeemerald's data/maps/map_groups.json) — mismo archivo que
/// server/internal/wildencounter usa para resolver tablas de encuentros por mapa.</summary>
internal sealed class MapEntry
{
    public int Group { get; set; }
    public int Num { get; set; }
    public string Id { get; set; } = "";
    public string Name { get; set; } = "";
}

/// <summary>
/// Traduce (mapGroup, mapNum) crudos —lo único que IMemoryAdapter.GetMapGroupAndNum() puede leer
/// de la ROM— al ID canónico de mapa ("MAP_ROUTE101") que el servidor espera en "move" y
/// "wild_encounter_triggered" (ver wildencounter.TryEncounter, que busca por ese string exacto en
/// data/pokemon/encounters.json). Antes de esto, Program.cs solo mandaba el nombre del mapa de
/// SPAWN (login_ok) sufijado con el byte crudo de mapNum para poder comparar "¿mismo mapa que
/// otro jugador?" por igualdad — suficiente para agrupar jugadores visualmente, pero nunca un ID
/// real, así que cualquier mapa que no fuera el de spawn quedaba invisible para lógica
/// server-side que necesita saber CUÁL mapa es (como las tablas de encuentros salvajes).
/// Se carga UNA vez al arrancar, igual que PokedexCatalog.
/// </summary>
internal static class MapCatalog
{
    private static readonly JsonSerializerOptions JsonOpts = new() { PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower };

    private static Dictionary<(int Group, int Num), string> _idByGroupNum = new();

    public static bool Loaded { get; private set; }

    public static void Load(string dataDir)
    {
        string path = Path.Combine(dataDir, "maps.json");
        var list = JsonSerializer.Deserialize<List<MapEntry>>(File.ReadAllText(path), JsonOpts) ?? [];

        var byGroupNum = new Dictionary<(int, int), string>(list.Count);
        foreach (var m in list) byGroupNum[(m.Group, m.Num)] = m.Id;

        _idByGroupNum = byGroupNum;
        Loaded = true;
    }

    /// <summary>Null si el catálogo no cargó, o si (group, num) no aparece en maps.json (mapas
    /// interiores/menores que gendata todavía no emite) — el llamador debe caer a su propio
    /// esquema de respaldo en ese caso, no asumir que esto siempre resuelve.</summary>
    public static string? IdFor(byte group, byte num) => _idByGroupNum.GetValueOrDefault((group, num));
}
