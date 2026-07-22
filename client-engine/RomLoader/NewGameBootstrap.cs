namespace RomLoader;

/// <summary>
/// Orquesta las escrituras necesarias para que un jugador arranque "ya adentro del mundo" —
/// dinero, inicial elegido y flags de historia — sin tener que jugar la intro de Emerald
/// (dormitorio, reloj, Birch en apuros). IDs de flag sacados de
/// include/constants/flags.h del código fuente real de pokeemerald, no adivinados.
///
/// MOMENTO CRÍTICO: hay que aplicar esto DESPUÉS de cargar el .sav (SaveRam) pero ANTES de que
/// el jugador confirme "Continuar" en la pantalla de título — ese es el punto donde el juego
/// copia SaveBlock1->playerParty a gPlayerParty (la copia "viva" que realmente se muestra en
/// pantalla durante el gameplay). Escribir solo en SaveBlock1 después de ese punto (partida ya
/// en curso) persiste bien pero no se ve reflejado hasta guardar/cargar de nuevo — confirmado
/// empíricamente: al escribir un Treecko de prueba a mitad de partida, la lectura via
/// GetParty() lo mostraba correcto pero el menú Pokémon en pantalla seguía mostrando el
/// Pokémon viejo hasta escribir también gPlayerParty (dirección fija 0x020244EC, no necesita
/// indirección de puntero). Para el flujo real ("nuevo personaje, arranca ya afuera") no debería
/// hacer falta tocar gPlayerParty: el juego todavía no lo pobló cuando se aplica el bootstrap.
/// </summary>
public static class NewGameBootstrap
{
    private const int SystemFlags = 0x860;
    public const int FlagSysPokemonGet = SystemFlags + 0x0;
    public const int FlagSysPokedexGet = SystemFlags + 0x1;
    public const int FlagSysClockSet = SystemFlags + 0x35;

    public static void Apply(IMemoryAdapter adapter, int startingMoney, ushort starterSpecies, string nickname, string otName, Random random)
    {
        adapter.SetMoney(startingMoney);
        adapter.SetPartyPokemon(0, StarterCatalog.BuildStarterSpec(starterSpecies, nickname, otName, random));
        adapter.SetFlag(FlagSysPokemonGet, true);
        adapter.SetFlag(FlagSysPokedexGet, true);
        adapter.SetFlag(FlagSysClockSet, true);
    }
}
