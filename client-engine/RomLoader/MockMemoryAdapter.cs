using LibretroCore;

namespace RomLoader;

/// <summary>
/// Implementación de IMemoryAdapter que NO lee memoria de ningún emulador — devuelve datos
/// sintéticos, para que el motor (red, chat, paneles, jugadores remotos) pueda arrancar y
/// probarse sin tener una ROM cargada. Esta es la prueba concreta de que el motor no está
/// atado a leer una ROM real: si esto compila y anda contra el mismo Program.cs que usa
/// GbaMemoryAdapter, la abstracción de IMemoryAdapter realmente separó al motor del RomLoader.
///
/// La posición "deambula" en un cuadradito chico en vez de quedar fija: sirve para ver en
/// vivo que el "move" sale hacia el servidor y que otro jugador conectado lo ve moverse,
/// sin depender de que un GBA real esté corriendo.
/// </summary>
public sealed class MockMemoryAdapter : IMemoryAdapter
{
    private static readonly (int Dx, int Dy)[] Path =
        [(1, 0), (1, 0), (0, 1), (0, 1), (-1, 0), (-1, 0), (0, -1), (0, -1)];

    private int _x = 10, _y = 12;
    private int _frame;

    public PlayerPosition GetPlayerPosition()
    {
        _frame++;
        if (_frame % 30 == 0)
        {
            var (dx, dy) = Path[(_frame / 30) % Path.Length];
            _x += dx;
            _y += dy;
        }
        return new PlayerPosition(_x, _y);
    }

    /// <summary>Mapa fijo — no hay transiciones simuladas, alcanza con un valor estable para
    /// probar el resto del motor (comparación por igualdad de mapa, ver Program.cs).</summary>
    public byte GetMapNumber() => 1;

    /// <summary>Sin ROM real no hay OAM que leer — null es una respuesta válida (ver el
    /// contrato de IMemoryAdapter): el motor simplemente no muestra sprite propio recapturado,
    /// nada se rompe.</summary>
    public OamEntry? FindPlayerSprite(IReadOnlyList<OamEntry> oamEntries) => null;

    /// <summary>Sin ROM real no hay SaveBlock1/2 que leer — null es una respuesta válida.</summary>
    public int? GetMoney() => null;

    /// <summary>Sin ROM real no hay equipo que leer — null es una respuesta válida.</summary>
    public IReadOnlyList<Gen3Codec.DecryptedPokemon>? GetParty() => null;

    /// <summary>Sin ROM real no hay nada que escribir — no-op, consistente con el resto del mock.</summary>
    public void SetMoney(int amount) { }

    public bool? GetFlag(int flagId) => null;
    public void SetFlag(int flagId, bool value) { }
    public void SetPartyPokemon(int slot, Gen3Codec.NewPokemonSpec spec) { }
}
