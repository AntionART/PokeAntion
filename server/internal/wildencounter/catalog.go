package wildencounter

import (
	"encoding/json"
	"fmt"
	"os"

	"pokemon-online/server/internal/pokemon"
)

// EncounterSlot/MapEncounters son la forma exacta del JSON que emite server/cmd/gendata
// (data/pokemon/encounters.json) — mismo criterio que pokemon.SpeciesInfo/battle.Move: datos
// reales de pokeemerald, no inventados.
type EncounterSlot struct {
	SpeciesID int `json:"species_id"`
	MinLevel  int `json:"min_level"`
	MaxLevel  int `json:"max_level"`
	Weight    int `json:"weight"`
}

// FishingSlot ver el comentario homónimo en server/cmd/gendata/main.go — la pesca tiene 3
// sub-tablas por caña que no se mezclan entre sí.
type FishingSlot struct {
	SpeciesID int    `json:"species_id"`
	MinLevel  int    `json:"min_level"`
	MaxLevel  int    `json:"max_level"`
	Weight    int    `json:"weight"`
	RodTier   string `json:"rod_tier"`
}

type MapEncounters struct {
	MapID                  string          `json:"map_id"`
	EncounterRate          int             `json:"encounter_rate"`
	Slots                  []EncounterSlot `json:"slots"`
	WaterEncounterRate     int             `json:"water_encounter_rate"`
	WaterSlots             []EncounterSlot `json:"water_slots"`
	RockSmashEncounterRate int             `json:"rock_smash_encounter_rate"`
	RockSmashSlots         []EncounterSlot `json:"rock_smash_slots"`
	FishingSlots           []FishingSlot   `json:"fishing_slots"`
}

var encountersByMap map[string]MapEncounters

// LoadCatalogs carga encounters.json/learnsets.json una sola vez al arrancar el servidor (ver
// main.go) — antes de eso, TryEncounter/pokemon.MovesForLevel no encuentran nada (mismo patrón
// que pokemon.LoadSpeciesCatalog/battle.LoadMoveCatalog). El learnset en sí vive en el paquete
// pokemon (ver internal/pokemon/learnsets.go — es una propiedad de la especie, no de un
// encuentro salvaje en particular), esta función solo delega esa parte para no duplicar la
// carga en dos paquetes.
func LoadCatalogs(encountersPath, learnsetsPath string) error {
	encData, err := os.ReadFile(encountersPath)
	if err != nil {
		return fmt.Errorf("leyendo catálogo de encuentros (%s): %w", encountersPath, err)
	}
	var rawEnc []MapEncounters
	if err := json.Unmarshal(encData, &rawEnc); err != nil {
		return fmt.Errorf("parseando catálogo de encuentros: %w", err)
	}
	byMap := make(map[string]MapEncounters, len(rawEnc))
	for _, e := range rawEnc {
		byMap[e.MapID] = e
	}
	encountersByMap = byMap

	return pokemon.LoadLearnsetCatalog(learnsetsPath)
}
