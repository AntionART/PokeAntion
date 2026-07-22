namespace RomLoader;

/// <summary>
/// Los 3 iniciales de Emerald con los datos exactos que hacen falta para armar un Pokémon
/// nivel 5 jugable desde cero (species ID, stats base, movimientos que ya sabría a nivel 1) —
/// sacados directo de <c>src/data/pokemon/species_info.h</c> y
/// <c>src/data/pokemon/level_up_learnsets.h</c> del código fuente real de pokeemerald
/// (Pokemon Esmeralda/pokeemerald-master), no adivinados ni recordados de memoria. Move IDs y
/// PP de include/constants/moves.h y src/data/battle_moves.h respectivamente.
///
/// Pensado para el "elegí tu inicial al crear personaje" estilo PokeMMO: RomLoader arma el
/// Pokémon con <see cref="Gen3Codec.BuildFullPartySlot(Gen3Codec.NewPokemonSpec)"/> y lo
/// escribe directo en el equipo, sin que el jugador tenga que jugar la intro para conseguirlo.
/// </summary>
public static class StarterCatalog
{
    public const ushort SpeciesTreecko = 277;
    public const ushort SpeciesTorchic = 280;
    public const ushort SpeciesMudkip = 283;

    private const ushort MovePound = 1;
    private const ushort MoveScratch = 10;
    private const ushort MoveTackle = 33;
    private const ushort MoveLeer = 43;
    private const ushort MoveGrowl = 45;

    public static readonly Gen3Codec.BaseStats TreeckoBaseStats = new()
    { Hp = 40, Attack = 45, Defense = 35, Speed = 70, SpAttack = 65, SpDefense = 55 };

    public static readonly Gen3Codec.BaseStats TorchicBaseStats = new()
    { Hp = 45, Attack = 60, Defense = 40, Speed = 45, SpAttack = 70, SpDefense = 50 };

    public static readonly Gen3Codec.BaseStats MudkipBaseStats = new()
    { Hp = 50, Attack = 70, Defense = 50, Speed = 40, SpAttack = 50, SpDefense = 50 };

    /// <summary>Movimientos que cada inicial ya sabe a nivel 1 (Pound/Leer, Scratch/Growl,
    /// Tackle/Growl) — los únicos relevantes para un inicial nivel 5, ya que el siguiente
    /// movimiento de cada uno se aprende recién a partir de nivel 6-7.</summary>
    public static (ushort MoveId, byte Pp)[] LevelOneMoves(ushort species) => species switch
    {
        SpeciesTreecko => [(MovePound, 35), (MoveLeer, 30)],
        SpeciesTorchic => [(MoveScratch, 35), (MoveGrowl, 40)],
        SpeciesMudkip => [(MoveTackle, 35), (MoveGrowl, 40)],
        _ => throw new ArgumentException($"Especie {species} no es uno de los 3 iniciales de Emerald."),
    };

    public static Gen3Codec.BaseStats BaseStatsFor(ushort species) => species switch
    {
        SpeciesTreecko => TreeckoBaseStats,
        SpeciesTorchic => TorchicBaseStats,
        SpeciesMudkip => MudkipBaseStats,
        _ => throw new ArgumentException($"Especie {species} no es uno de los 3 iniciales de Emerald."),
    };

    /// <summary>Arma la especificación completa de un inicial nivel 5 recién elegido, lista
    /// para <see cref="Gen3Codec.BuildFullPartySlot(Gen3Codec.NewPokemonSpec)"/>. personality/otId
    /// se generan al azar (determinan la clave de cifrado, no necesitan "significar" nada).</summary>
    public static Gen3Codec.NewPokemonSpec BuildStarterSpec(ushort species, string nickname, string otName, Random random)
    {
        return new Gen3Codec.NewPokemonSpec
        {
            Species = species,
            Level = 5,
            Personality = (uint)random.NextInt64(uint.MaxValue),
            OtId = (uint)random.NextInt64(uint.MaxValue),
            Nickname = nickname,
            OtName = otName,
            Moves = LevelOneMoves(species),
            BaseStats = BaseStatsFor(species),
        };
    }
}
