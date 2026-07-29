package pokemon

// Los 3 iniciales de Emerald — mismos datos exactos (species ID, stats base, movimientos de
// nivel 1) que client-engine/RomLoader/StarterCatalog.cs, sacados del código fuente real de
// pokeemerald (src/data/pokemon/species_info.h, level_up_learnsets.h), no adivinados. Se
// duplican acá (Go, servidor) en vez de compartir un archivo porque el cliente y el servidor
// están en lenguajes distintos — si el catálogo crece (más iniciales, más especies) vale la
// pena generar ambos desde una fuente común, pero para 3 entradas no se justifica todavía.
const (
	SpeciesTreecko = 277
	SpeciesTorchic = 280
	SpeciesMudkip  = 283
)

type BaseStats struct {
	HP, Attack, Defense, Speed, SpAttack, SpDefense int
}

type move struct {
	MoveID int
	PP     int
}

// Movimientos que cada inicial ya sabe a nivel 1 (los únicos relevantes a nivel 5): Pound/Leer,
// Scratch/Growl, Tackle/Growl. Move IDs de include/constants/moves.h, PP de src/data/battle_moves.h.
// Coincide exactamente con data/pokemon/learnsets.json (generado por server/cmd/gendata desde
// level_up_learnsets.h, agregado para armar el moveset de Pokémon salvajes atrapados, ver
// server/internal/wildencounter) — se deja hardcodeado acá en vez de leer ese archivo porque
// AddStarter es un caso fijo de 3 especies a nivel 5 siempre, no vale la pena la indirección.
var starterMoves = map[int][2]move{
	SpeciesTreecko: {{MoveID: 1, PP: 35}, {MoveID: 43, PP: 30}},
	SpeciesTorchic: {{MoveID: 10, PP: 35}, {MoveID: 45, PP: 40}},
	SpeciesMudkip:  {{MoveID: 33, PP: 35}, {MoveID: 45, PP: 40}},
}

// El nombre y las estadísticas base de cada inicial ya NO se hardcodean acá — salen del
// catálogo completo (ver species_catalog.go: SpeciesName/SpeciesBaseStats), la misma fuente
// que cualquier otra especie.

func IsValidStarter(species int) bool {
	return species == SpeciesTreecko || species == SpeciesTorchic || species == SpeciesMudkip
}

// ComputeStatsAtLevel: misma fórmula que Gen3Codec.ComputeStatsAtLevel (C#) — estándar de Gen3
// (idéntica en todas las generaciones 3-7, no específica de esta ROM). IVs=0/EVs=0/naturaleza
// neutra, misma simplificación deliberada que el lado cliente.
func ComputeStatsAtLevel(b BaseStats, level int) (hp, attack, defense, speed, spAttack, spDefense int) {
	other := func(base int) int { return (2*base)*level/100 + 5 }
	hp = (2*b.HP)*level/100 + level + 10
	attack = other(b.Attack)
	defense = other(b.Defense)
	speed = other(b.Speed)
	spAttack = other(b.SpAttack)
	spDefense = other(b.SpDefense)
	return
}

// ComputeStatsWithIVs: misma fórmula real de Gen3, pero con IVs reales (no todos en 0) — para
// un Pokémon salvaje atrapado de verdad (ver AddCaught), a diferencia de un inicial regalado
// (AddStarter, que sigue en IV=0 sin cambios). EVs siguen en 0 (recién atrapado, sin entrenar).
func ComputeStatsWithIVs(b BaseStats, level int, ivs [6]int) (hp, attack, defense, speed, spAttack, spDefense int) {
	other := func(base, iv int) int { return (2*base+iv)*level/100 + 5 }
	hp = (2*b.HP+ivs[0])*level/100 + level + 10
	attack = other(b.Attack, ivs[1])
	defense = other(b.Defense, ivs[2])
	speed = other(b.Speed, ivs[3])
	spAttack = other(b.SpAttack, ivs[4])
	spDefense = other(b.SpDefense, ivs[5])
	return
}
