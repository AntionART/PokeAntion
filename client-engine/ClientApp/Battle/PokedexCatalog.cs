using System.Text.Json;

namespace ClientApp.Battle;

/// <summary>Forma exacta de una entrada de data/pokemon/species.json (generado por
/// server/cmd/gendata desde el código fuente real de pokeemerald) — mismo archivo que lee el
/// servidor (server/internal/pokemon/species_catalog.go), una sola fuente de verdad para
/// ambos lados en vez de tablas hardcodeadas separadas que podían desincronizarse.</summary>
internal sealed class SpeciesEntry
{
    public int Id { get; set; }
    public string Name { get; set; } = "";
    public string SpriteFolder { get; set; } = "";
    public int Type1 { get; set; }
    public int Type2 { get; set; }
    public int BaseHp { get; set; }
    public int BaseAttack { get; set; }
    public int BaseDefense { get; set; }
    public int BaseSpAttack { get; set; }
    public int BaseSpDefense { get; set; }
    public int BaseSpeed { get; set; }
}

/// <summary>Forma exacta de una entrada de data/pokemon/moves.json — ver battle.Move en el
/// servidor (server/internal/battle/moves.go), mismo archivo fuente.</summary>
internal sealed class MoveEntry
{
    public int Id { get; set; }
    public string Name { get; set; } = "";
    public int Type { get; set; }
    public int Power { get; set; }
    public int Accuracy { get; set; }
    public int Pp { get; set; }
    public int Effect { get; set; }
    public bool TargetsSelf { get; set; }
}

/// <summary>
/// Catálogo completo de especies (~386) y movimientos (~355) — reemplaza los diccionarios
/// hardcodeados de 3 especies/5 movimientos que SpriteCatalog/MoveCatalog tenían antes de que
/// existiera data/pokemon/*.json. Se carga UNA vez al arrancar (ver Program.cs), no en cada
/// consulta — los archivos no cambian durante la ejecución.
/// </summary>
internal static class PokedexCatalog
{
    private static readonly JsonSerializerOptions JsonOpts = new() { PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower };

    private static Dictionary<int, SpeciesEntry> _species = new();
    private static Dictionary<int, MoveEntry> _moves = new();

    public static bool Loaded { get; private set; }

    public static void Load(string dataDir)
    {
        var speciesList = JsonSerializer.Deserialize<List<SpeciesEntry>>(
            File.ReadAllText(Path.Combine(dataDir, "species.json")), JsonOpts) ?? [];
        var movesList = JsonSerializer.Deserialize<List<MoveEntry>>(
            File.ReadAllText(Path.Combine(dataDir, "moves.json")), JsonOpts) ?? [];

        var species = new Dictionary<int, SpeciesEntry>(speciesList.Count);
        foreach (var s in speciesList) species[s.Id] = s;
        var moves = new Dictionary<int, MoveEntry>(movesList.Count);
        foreach (var m in movesList) moves[m.Id] = m;

        _species = species;
        _moves = moves;
        Loaded = true;
    }

    public static SpeciesEntry? Species(int id) => _species.TryGetValue(id, out var s) ? s : null;
    public static MoveEntry? Move(int id) => _moves.TryGetValue(id, out var m) ? m : null;
}
