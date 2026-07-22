namespace ClientApp.Battle;

/// <summary>
/// Nombres reales de los únicos movimientos que puede tener un Pokémon en batalla hoy: los
/// que un inicial ya sabe a nivel 5 (ver server/internal/battle/moves.go y
/// server/internal/pokemon/starters.go, mismos IDs verificados contra include/constants/
/// moves.h). No hay catálogo completo de movimientos en el cliente todavía — un ID fuera de
/// esta lista no debería aparecer mientras solo los 3 iniciales puedan pelear.
/// </summary>
internal static class MoveCatalog
{
    private static readonly Dictionary<int, string> NameById = new()
    {
        [1] = "Pound",
        [10] = "Scratch",
        [33] = "Tackle",
        [43] = "Leer",
        [45] = "Growl",
    };

    public static string NameOf(int moveId) => moveId == 0 ? "-" : (NameById.TryGetValue(moveId, out var n) ? n : $"Movimiento #{moveId}");
}
