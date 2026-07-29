package pokemon

import (
	"encoding/json"
	"fmt"
	"os"
)

// LearnsetMove/speciesLearnset son la forma exacta del JSON que emite server/cmd/gendata
// (data/pokemon/learnsets.json) — qué movimiento aprende cada especie a qué nivel, real de
// pokeemerald (level_up_learnsets.h). Vive en este paquete (no en wildencounter, que es el
// único llamador hoy) porque "qué aprende un Pokémon al subir de nivel" es una propiedad de la
// especie/Pokémon, igual que sus stats base — el mismo criterio que ya separa SpeciesInfo acá.
type LearnsetMove struct {
	Level  int `json:"level"`
	MoveID int `json:"move_id"`
}

type speciesLearnset struct {
	SpeciesID int            `json:"species_id"`
	Moves     []LearnsetMove `json:"moves"`
}

var learnsetsBySpecies map[int][]LearnsetMove

// LoadLearnsetCatalog carga learnsets.json una sola vez al arrancar el servidor (ver main.go),
// antes de eso MovesForLevel/NewMovesLearnedBetween no encuentran nada (mismo patrón que
// LoadSpeciesCatalog).
func LoadLearnsetCatalog(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("leyendo catálogo de learnsets (%s): %w", path, err)
	}
	var raw []speciesLearnset
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parseando catálogo de learnsets: %w", err)
	}
	bySpecies := make(map[int][]LearnsetMove, len(raw))
	for _, l := range raw {
		bySpecies[l.SpeciesID] = l.Moves
	}
	learnsetsBySpecies = bySpecies
	return nil
}

// MovesForLevel devuelve hasta los 4 movimientos MÁS RECIENTES que la especie ya aprendió a ese
// nivel o antes (mismo criterio que el juego real al generar un Pokémon nuevo) — vacío si la
// especie no tiene learnset conocido (no debería pasar con datos reales del catálogo). Usado
// para armar el moveset inicial de un Pokémon salvaje (ver wildencounter.NewWildFighter).
func MovesForLevel(species, level int) []int {
	learnset := learnsetsBySpecies[species]
	var known []int
	for _, m := range learnset {
		if m.Level <= level {
			known = append(known, m.MoveID)
		}
	}
	if len(known) > 4 {
		known = known[len(known)-4:]
	}
	return known
}

// NewMovesLearnedBetween devuelve los movimientos que se aprenden estrictamente DESPUÉS de
// oldLevel y hasta newLevel inclusive, en orden de nivel — para cuando AddExperience salta
// varios niveles de una sola vez (una sola pelea puede dar exp de sobra para subir 2-3 niveles),
// hay que revisar TODO el rango salteado, no solo el nivel final.
func NewMovesLearnedBetween(species, oldLevel, newLevel int) []int {
	learnset := learnsetsBySpecies[species]
	var out []int
	for _, m := range learnset {
		if m.Level > oldLevel && m.Level <= newLevel {
			out = append(out, m.MoveID)
		}
	}
	return out
}
