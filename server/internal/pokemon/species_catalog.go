package pokemon

import (
	"encoding/json"
	"fmt"
	"os"
)

// SpeciesInfo es la forma exacta del JSON que emite server/cmd/gendata (data/pokemon/species.json),
// generado del código fuente real de pokeemerald — no datos adivinados ni transcritos a mano.
type SpeciesInfo struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	SpriteFolder  string `json:"sprite_folder"`
	Type1         int    `json:"type1"`
	Type2         int    `json:"type2"`
	BaseHP        int    `json:"base_hp"`
	BaseAttack    int    `json:"base_attack"`
	BaseDefense   int    `json:"base_defense"`
	BaseSpAttack  int    `json:"base_sp_attack"`
	BaseSpDefense int    `json:"base_sp_defense"`
	BaseSpeed     int    `json:"base_speed"`
	CatchRate     int    `json:"catch_rate"`
	ExpYield      int    `json:"exp_yield"`
	// GrowthRate: ID real de include/constants/pokemon.h (GROWTH_MEDIUM_FAST=0, GROWTH_ERRATIC=1,
	// GROWTH_FLUCTUATING=2, GROWTH_MEDIUM_SLOW=3, GROWTH_FAST=4, GROWTH_SLOW=5) — qué curva de
	// experiencia real usa esta especie (ver expForLevel en pokemon.go).
	GrowthRate int `json:"growth_rate"`
}

// speciesCatalog cubre las ~386 especies reales de Emerald (antes solo cubría los 3 iniciales,
// a mano, ver starters.go) — sin esto, un Pokémon de cualquier especie que no fuera un inicial
// no podía pelear (SpeciesTypes no lo conocía) ni comerciarse con un nombre real.
var speciesCatalog map[int]SpeciesInfo

// LoadSpeciesCatalog carga el catálogo completo una sola vez al arrancar el servidor (ver
// main.go), antes de que cualquier registro/batalla pueda necesitar datos de una especie.
func LoadSpeciesCatalog(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("leyendo catálogo de especies (%s): %w", path, err)
	}
	var raw []SpeciesInfo
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parseando catálogo de especies: %w", err)
	}
	catalog := make(map[int]SpeciesInfo, len(raw))
	for _, s := range raw {
		catalog[s.ID] = s
	}
	speciesCatalog = catalog
	return nil
}

// SpeciesTypes devuelve (tipo1, tipo2) de una especie — tipo2 == tipo1 si es de un solo tipo,
// mismo criterio que battle.Combatant. IDs numéricos de tipo idénticos a battle.TypeXxx
// (ambos vienen, por caminos distintos, de include/constants/pokemon.h de pokeemerald — no se
// importa el paquete battle acá para evitar un ciclo pokemon<->battle).
func SpeciesTypes(species int) (int, int) {
	s, ok := speciesCatalog[species]
	if !ok {
		return 0, 0 // TypeNormal/TypeNormal — no debería pasar con un species_id real del catálogo
	}
	return s.Type1, s.Type2
}

// SpeciesBaseStats devuelve las 6 estadísticas base reales de una especie, para calcular sus
// stats a cualquier nivel (ver ComputeStatsAtLevel). ok=false si la especie no está en el
// catálogo (species_id inválido o corrupto).
func SpeciesBaseStats(species int) (b BaseStats, ok bool) {
	s, found := speciesCatalog[species]
	if !found {
		return BaseStats{}, false
	}
	return BaseStats{
		HP: s.BaseHP, Attack: s.BaseAttack, Defense: s.BaseDefense,
		Speed: s.BaseSpeed, SpAttack: s.BaseSpAttack, SpDefense: s.BaseSpDefense,
	}, true
}

// SpeciesCatchRate/SpeciesExpYield: para encuentros salvajes (ver server/internal/wildencounter)
// — fórmula real de captura de Gen3 y experiencia otorgada al vencer, respectivamente.
func SpeciesCatchRate(species int) (int, bool) {
	s, ok := speciesCatalog[species]
	return s.CatchRate, ok
}

func SpeciesExpYield(species int) (int, bool) {
	s, ok := speciesCatalog[species]
	return s.ExpYield, ok
}

// SpeciesGrowthRate devuelve la curva de experiencia real de una especie (ver expForLevel).
func SpeciesGrowthRate(species int) (int, bool) {
	s, ok := speciesCatalog[species]
	return s.GrowthRate, ok
}

// SpeciesName devuelve el nombre real de una especie (ej. "TREECKO"), o un placeholder si no
// está en el catálogo — usado donde antes solo se mostraba el species_id numérico crudo.
func SpeciesName(species int) string {
	if s, ok := speciesCatalog[species]; ok {
		return s.Name
	}
	return fmt.Sprintf("#%d", species)
}
