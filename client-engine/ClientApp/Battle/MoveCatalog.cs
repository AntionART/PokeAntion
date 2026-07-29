namespace ClientApp.Battle;

/// <summary>
/// Nombres reales de movimientos — sale de PokedexCatalog (data/pokemon/moves.json, las ~355
/// entradas reales de pokeemerald), no de una tabla hardcodeada de los 5 movimientos de los
/// 3 iniciales como antes.
/// </summary>
internal static class MoveCatalog
{
    public static string NameOf(int moveId)
    {
        if (moveId == 0) return "-";
        var entry = PokedexCatalog.Move(moveId);
        return entry?.Name ?? $"Movimiento #{moveId}";
    }
}
