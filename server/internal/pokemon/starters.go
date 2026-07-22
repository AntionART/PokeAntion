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

type baseStats struct {
	HP, Attack, Defense, Speed, SpAttack, SpDefense int
}

type move struct {
	MoveID int
	PP     int
}

var starterBaseStats = map[int]baseStats{
	SpeciesTreecko: {HP: 40, Attack: 45, Defense: 35, Speed: 70, SpAttack: 65, SpDefense: 55},
	SpeciesTorchic: {HP: 45, Attack: 60, Defense: 40, Speed: 45, SpAttack: 70, SpDefense: 50},
	SpeciesMudkip:  {HP: 50, Attack: 70, Defense: 50, Speed: 40, SpAttack: 50, SpDefense: 50},
}

// Movimientos que cada inicial ya sabe a nivel 1 (los únicos relevantes a nivel 5): Pound/Leer,
// Scratch/Growl, Tackle/Growl. Move IDs de include/constants/moves.h, PP de src/data/battle_moves.h.
var starterMoves = map[int][2]move{
	SpeciesTreecko: {{MoveID: 1, PP: 35}, {MoveID: 43, PP: 30}},
	SpeciesTorchic: {{MoveID: 10, PP: 35}, {MoveID: 45, PP: 40}},
	SpeciesMudkip:  {{MoveID: 33, PP: 35}, {MoveID: 45, PP: 40}},
}

// starterName es el nombre que el propio juego le pone a un Pokémon sin apodo (nombre de la
// especie en mayúsculas) — no hay tabla de nombres en el servidor todavía, así que se hardcodea
// solo para estos 3, en vez de traer toda la tabla de species_info.h por un nombre de display.
var starterName = map[int]string{
	SpeciesTreecko: "TREECKO",
	SpeciesTorchic: "TORCHIC",
	SpeciesMudkip:  "MUDKIP",
}

// Tipo(s) de cada inicial — IDs numéricos idénticos a battle.TypeXxx (no se importa ese
// paquete acá para evitar un ciclo pokemon<->battle; son constantes de include/constants/
// pokemon.h de pokeemerald, no una decisión de diseño nuestra, así que duplicarlas como
// números crudos con este comentario alcanza). 12=Grass, 10=Fire, 11=Water.
var starterTypes = map[int][2]int{
	SpeciesTreecko: {12, 12},
	SpeciesTorchic: {10, 10},
	SpeciesMudkip:  {11, 11},
}

// SpeciesTypes devuelve (tipo1, tipo2) de una especie soportada (ver IsValidStarter) — tipo2
// == tipo1 si es de un solo tipo, mismo criterio que battle.Combatant.
func SpeciesTypes(species int) (int, int) {
	t := starterTypes[species]
	return t[0], t[1]
}

func IsValidStarter(species int) bool {
	_, ok := starterBaseStats[species]
	return ok
}

// computeStatsAtLevel: misma fórmula que Gen3Codec.ComputeStatsAtLevel (C#) — estándar de Gen3
// (idéntica en todas las generaciones 3-7, no específica de esta ROM). IVs=0/EVs=0/naturaleza
// neutra, misma simplificación deliberada que el lado cliente.
func computeStatsAtLevel(b baseStats, level int) (hp, attack, defense, speed, spAttack, spDefense int) {
	other := func(base int) int { return (2*base)*level/100 + 5 }
	hp = (2*b.HP)*level/100 + level + 10
	attack = other(b.Attack)
	defense = other(b.Defense)
	speed = other(b.Speed)
	spAttack = other(b.SpAttack)
	spDefense = other(b.SpDefense)
	return
}
