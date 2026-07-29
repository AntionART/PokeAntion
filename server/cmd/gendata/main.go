// Comando de desarrollo (no se corre en producción): parsea el código fuente REAL de
// pokeemerald (checkout local en "Pokemon Esmeralda/pokeemerald-master/pokeemerald-master",
// mismo que ya se usó para verificar a mano los 3 iniciales y sus 5 movimientos) y genera
// data/pokemon/species.json y data/pokemon/moves.json — el catálogo completo (~386 especies,
// ~354 movimientos) que hasta ahora solo cubría los 3 iniciales, transcritos a mano uno por
// uno. Portar ~740 entradas a mano no es viable ni confiable (ver gba_memory_scanning_method:
// nunca adivinar/recordar de memoria) — un parser mecánico sobre el mismo archivo fuente que
// ya se usó como referencia es la forma correcta de escalar eso.
//
// Uso: go run ./cmd/gendata <path-al-checkout-de-pokeemerald>
// Sin argumento, asume la ubicación relativa de este repo.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type SpeciesEntry struct {
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
	// CatchRate/ExpYield: para encuentros salvajes (server/internal/wildencounter) — fórmula de
	// captura real de Gen3 y experiencia otorgada al vencer, ambos del mismo bloque de
	// species_info.h que ya se lee para el resto de las stats.
	CatchRate int `json:"catch_rate"`
	ExpYield  int `json:"exp_yield"`
	// GrowthRate es el ID real de include/constants/pokemon.h (GROWTH_MEDIUM_FAST=0,
	// GROWTH_ERRATIC=1, GROWTH_FLUCTUATING=2, GROWTH_MEDIUM_SLOW=3, GROWTH_FAST=4,
	// GROWTH_SLOW=5) — server/internal/pokemon usa esto para la curva de experiencia real de
	// CADA especie en vez de una única curva universal (ver AddExperience).
	GrowthRate int `json:"growth_rate"`
}

// MapEntry es una entrada de data/pokemon/maps.json: la tabla (mapGroup, mapNum) -> identidad
// real de mapa que hasta esta sesión no existía (ver memory-maps/*.json player._map_note) — sale
// de data/maps/map_groups.json (el orden de grupo/mapa ES mapGroup/mapNum, ver include/global.h
// struct WarpData) cruzado con el "id"/"name" real de cada data/maps/<Carpeta>/map.json.
type MapEntry struct {
	Group int    `json:"group"`
	Num   int    `json:"num"`
	ID    string `json:"id"`   // "MAP_ROUTE101"
	Name  string `json:"name"` // "Route101"
}

// LearnsetMove/SpeciesLearnset: qué movimientos aprende una especie por nivel (para armar el
// moveset de un Pokémon salvaje recién generado, ver server/internal/wildencounter) — de
// level_up_learnsets.h + level_up_learnset_pointers.h.
type LearnsetMove struct {
	Level  int `json:"level"`
	MoveID int `json:"move_id"`
}

type SpeciesLearnset struct {
	SpeciesID int            `json:"species_id"`
	Moves     []LearnsetMove `json:"moves"`
}

// EncounterSlot/MapEncounters: tabla de encuentros salvajes por mapa (land_mons/water_mons/
// rock_smash_mons/fishing_mons) — de src/data/wild_encounters.json, que ya es JSON válido (no
// hace falta parsear C).
type EncounterSlot struct {
	SpeciesID int `json:"species_id"`
	MinLevel  int `json:"min_level"`
	MaxLevel  int `json:"max_level"`
	Weight    int `json:"weight"` // de encounter_rates, posicional (fijo por tipo de encuentro)
}

// FishingSlot es un EncounterSlot + a qué caña (RodTier) pertenece — a diferencia de
// land/water/rock_smash (un solo conjunto de slots por mapa), la pesca tiene 3 sub-tablas
// (old_rod/good_rod/super_rod) que NO se mezclan entre sí: con la caña vieja, solo se puede
// sacar lo que esa caña ofrece, nunca algo de good_rod/super_rod. Ver "groups" en el bloque
// "fields" de wild_encounters.json.
type FishingSlot struct {
	SpeciesID int    `json:"species_id"`
	MinLevel  int    `json:"min_level"`
	MaxLevel  int    `json:"max_level"`
	Weight    int    `json:"weight"`
	RodTier   string `json:"rod_tier"` // "old_rod" | "good_rod" | "super_rod"
}

type MapEncounters struct {
	MapID         string          `json:"map_id"` // "MAP_ROUTE101"
	EncounterRate int             `json:"encounter_rate"`
	Slots         []EncounterSlot `json:"slots"`
	// WaterEncounterRate/WaterSlots: encuentros surfeando (HM Surf) — mismo mecanismo que land,
	// tabla y probabilidad separadas. Vacío (encounter_rate=0) si el mapa no tiene agua.
	WaterEncounterRate int             `json:"water_encounter_rate,omitempty"`
	WaterSlots         []EncounterSlot `json:"water_slots,omitempty"`
	// RockSmashEncounterRate/RockSmashSlots: encuentros al usar Rock Smash sobre una roca
	// rompible. Vacío si el mapa no tiene rocas de ese tipo.
	RockSmashEncounterRate int             `json:"rock_smash_encounter_rate,omitempty"`
	RockSmashSlots         []EncounterSlot `json:"rock_smash_slots,omitempty"`
	// FishingSlots: pescando con caña. Sin FishingEncounterRate acá a propósito: en
	// src/wild_encounter.c la elección de especie (ChooseWildMonIndex_Fishing) no tiene un
	// chequeo de tasa propio como land/water/rock_smash — el "¿pica o no?" real vive en el
	// script de campo de la caña (fuera de wild_encounter.c, no rastreado acá), y de todos
	// modos este proyecto todavía no tiene ítem de caña ni la animación de pescar implementada
	// del lado del cliente — limitación conocida, no una fórmula ya resuelta y omitida.
	FishingSlots []FishingSlot `json:"fishing_slots,omitempty"`
}

type MoveEntry struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Type     int    `json:"type"`
	Power    int    `json:"power"`
	Accuracy int    `json:"accuracy"`
	PP       int    `json:"pp"`
	// Effect es el ID crudo de EFFECT_* de pokeemerald (include/constants/battle_move_effects.h)
	// — el motor de batalla (server/internal/battle) solo interpreta un subconjunto (daño
	// normal + bajar una estadística en -1), todo lo demás (status/clima/drenado/OHKO/etc.)
	// se guarda tal cual para poder ampliarse después, pero hoy se resuelve como "sin efecto
	// mecánico" si no hace daño, o como golpe normal si power > 0. Ver battle/moves.go.
	Effect int `json:"effect"`
	// TargetsSelf: true si el movimiento afecta a quien lo usa (ej. Swords Dance sube el
	// propio Ataque) en vez del rival (ej. Growl baja el Ataque rival) — necesario para
	// aplicar los efectos de subir/bajar estadística al lado correcto (ver battle/battle.go).
	// Viene de .target == MOVE_TARGET_USER en battle_moves.h; cualquier otro valor (SELECTED/
	// BOTH/RANDOM/DEPENDS) apunta al rival en un 1v1 sin aliados, que es todo lo que hay hoy.
	TargetsSelf bool `json:"targets_self"`
}

func main() {
	root := "../Pokemon Esmeralda/pokeemerald-master/pokeemerald-master"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	typeIDs := parseDefines(filepath.Join(root, "include/constants/pokemon.h"), "TYPE_")
	speciesIDs := parseDefines(filepath.Join(root, "include/constants/species.h"), "SPECIES_")
	moveIDs := parseDefines(filepath.Join(root, "include/constants/moves.h"), "MOVE_")
	effectIDs := parseDefines(filepath.Join(root, "include/constants/battle_move_effects.h"), "EFFECT_")

	speciesNames := parseNameTable(filepath.Join(root, "src/data/text/species_names.h"), "SPECIES_")
	moveNames := parseNameTable(filepath.Join(root, "src/data/text/move_names.h"), "MOVE_")

	spritesDir := filepath.Join(root, "graphics/pokemon")

	growthIDs := parseDefines(filepath.Join(root, "include/constants/pokemon.h"), "GROWTH_")
	species := parseSpeciesInfo(filepath.Join(root, "src/data/pokemon/species_info.h"), speciesIDs, speciesNames, typeIDs, growthIDs, spritesDir)
	moves := parseBattleMoves(filepath.Join(root, "src/data/battle_moves.h"), moveIDs, moveNames, typeIDs, effectIDs)
	maps := parseMaps(root)
	learnsets := parseLearnsets(root, speciesIDs, moveIDs)
	encounters := parseEncounters(filepath.Join(root, "src/data/wild_encounters.json"), speciesIDs)

	writeJSON("../data/pokemon/species.json", species)
	writeJSON("../data/pokemon/moves.json", moves)
	writeJSON("../data/pokemon/maps.json", maps)
	writeJSON("../data/pokemon/learnsets.json", learnsets)
	writeJSON("../data/pokemon/encounters.json", encounters)
	fmt.Printf("Generado: %d especies, %d movimientos, %d mapas, %d learnsets, %d mapas con encuentros\n",
		len(species), len(moves), len(maps), len(learnsets), len(encounters))
}

// parseDefines lee líneas "#define PREFIJO_NOMBRE 123" (ignora las que no son un entero
// plano, ej. "#define SPECIES_UNOWN_B (NUM_SPECIES + 1)") y devuelve nombre completo -> ID.
func parseDefines(path, prefix string) map[string]int {
	out := make(map[string]int)
	re := regexp.MustCompile(`^#define\s+(` + regexp.QuoteMeta(prefix) + `\w+)\s+(-?\d+)\s*$`)
	for _, line := range readLines(path) {
		m := re.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		out[m[1]] = n
	}
	return out
}

// parseNameTable lee entradas "[PREFIJO_X] = _("NOMBRE"),".
func parseNameTable(path, prefix string) map[string]string {
	out := make(map[string]string)
	re := regexp.MustCompile(`\[(` + regexp.QuoteMeta(prefix) + `\w+)\]\s*=\s*_\("([^"]*)"\)`)
	for _, line := range readLines(path) {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out[m[1]] = m[2]
	}
	return out
}

var (
	fieldRe   = regexp.MustCompile(`^\s*\.(\w+)\s*=\s*(.+?),?\s*$`)
	sectionRe = regexp.MustCompile(`^\s*\[(\w+)\]\s*=\s*$`)
)

func parseSpeciesInfo(path string, speciesIDs map[string]int, names map[string]string, typeIDs map[string]int, growthIDs map[string]int, spritesDir string) []SpeciesEntry {
	lines := readLines(path)
	var out []SpeciesEntry

	for i := 0; i < len(lines); i++ {
		m := sectionRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		macro := m[1]
		id, ok := speciesIDs[macro]
		if !ok || id == 0 {
			continue
		}

		fields := map[string]string{}
		var type1Name, type2Name string
		depth := 0
		for j := i + 1; j < len(lines); j++ {
			line := lines[j]
			depth += strings.Count(line, "{") - strings.Count(line, "}")

			if fm := fieldRe.FindStringSubmatch(line); fm != nil {
				key, val := fm[1], fm[2]
				if key == "types" {
					tm := regexp.MustCompile(`\{\s*(TYPE_\w+)\s*,\s*(TYPE_\w+)\s*\}`).FindStringSubmatch(val)
					if tm != nil {
						type1Name, type2Name = tm[1], tm[2]
					}
				} else {
					fields[key] = val
				}
			}
			if depth <= 0 && j > i+1 {
				break
			}
		}

		name := names[macro]
		if name == "" {
			continue // sin nombre real no vale la pena listar la especie (ej. entradas vacías/placeholder)
		}

		folder := strings.ToLower(strings.TrimPrefix(macro, "SPECIES_"))
		if _, err := os.Stat(filepath.Join(spritesDir, folder)); err != nil {
			folder = "" // sin sprite extraído para esta especie (formas especiales, etc.) — el cliente cae a un placeholder
		}

		out = append(out, SpeciesEntry{
			ID: id, Name: name, SpriteFolder: folder,
			Type1: typeIDs[type1Name], Type2: typeIDs[type2Name],
			BaseHP: atoi(fields["baseHP"]), BaseAttack: atoi(fields["baseAttack"]), BaseDefense: atoi(fields["baseDefense"]),
			BaseSpAttack: atoi(fields["baseSpAttack"]), BaseSpDefense: atoi(fields["baseSpDefense"]), BaseSpeed: atoi(fields["baseSpeed"]),
			CatchRate: atoi(fields["catchRate"]), ExpYield: atoi(fields["expYield"]),
			GrowthRate: growthIDs[fields["growthRate"]],
		})
	}
	return out
}

func parseBattleMoves(path string, moveIDs map[string]int, names map[string]string, typeIDs, effectIDs map[string]int) []MoveEntry {
	lines := readLines(path)
	var out []MoveEntry

	for i := 0; i < len(lines); i++ {
		m := sectionRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		macro := m[1]
		id, ok := moveIDs[macro]
		if !ok {
			continue
		}

		fields := map[string]string{}
		depth := 0
		for j := i + 1; j < len(lines); j++ {
			line := lines[j]
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if fm := fieldRe.FindStringSubmatch(line); fm != nil {
				fields[fm[1]] = fm[2]
			}
			if depth <= 0 && j > i+1 {
				break
			}
		}

		name := names[macro]
		if name == "" {
			name = strings.TrimPrefix(macro, "MOVE_")
		}

		out = append(out, MoveEntry{
			ID: id, Name: name,
			Type: typeIDs[fields["type"]], Power: atoi(fields["power"]), Accuracy: atoi(fields["accuracy"]), PP: atoi(fields["pp"]),
			Effect: effectIDs[fields["effect"]], TargetsSelf: strings.TrimSpace(fields["target"]) == "MOVE_TARGET_USER",
		})
	}
	return out
}

// parseMaps recorre data/maps/map_groups.json en orden (el índice de grupo y el índice dentro
// de cada grupo SON mapGroup/mapNum, confirmado contra struct WarpData en include/global.h) y
// para cada mapa lee su data/maps/<Carpeta>/map.json para el "id"/"name" reales — no hay que
// adivinar la convención de nombres, cada mapa ya declara su propio MAP_XXX explícitamente.
func parseMaps(root string) []MapEntry {
	data, err := os.ReadFile(filepath.Join(root, "data/maps/map_groups.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "no se pudo leer map_groups.json: %v\n", err)
		os.Exit(1)
	}
	// map_groups.json tiene los arrays de cada grupo como claves de nivel superior (el nombre
	// del grupo), no anidados bajo una clave "groups" — se decodifica genérico y se recupera
	// cada array por su nombre de grupo.
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(data, &generic); err != nil {
		fmt.Fprintf(os.Stderr, "parseando map_groups.json: %v\n", err)
		os.Exit(1)
	}
	var groupOrder []string
	if err := json.Unmarshal(generic["group_order"], &groupOrder); err != nil {
		fmt.Fprintf(os.Stderr, "parseando group_order: %v\n", err)
		os.Exit(1)
	}

	var out []MapEntry
	for groupIdx, groupName := range groupOrder {
		var mapNames []string
		if rawGroup, ok := generic[groupName]; ok {
			if err := json.Unmarshal(rawGroup, &mapNames); err != nil {
				continue
			}
		}
		for numIdx, mapName := range mapNames {
			mapJSONPath := filepath.Join(root, "data/maps", mapName, "map.json")
			mapData, err := os.ReadFile(mapJSONPath)
			if err != nil {
				continue // algunos nombres son layouts/dinámicos sin carpeta propia (MAP_DYNAMIC, etc.)
			}
			var mapHeader struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if err := json.Unmarshal(mapData, &mapHeader); err != nil {
				continue
			}
			out = append(out, MapEntry{Group: groupIdx, Num: numIdx, ID: mapHeader.ID, Name: mapHeader.Name})
		}
	}
	return out
}

// parseLearnsets junta level_up_learnset_pointers.h (qué array de movimientos usa cada especie)
// con level_up_learnsets.h (el array en sí: LEVEL_UP_MOVE(nivel, MOVE_X) por línea) — para armar
// el moveset de un Pokémon salvaje según su nivel (ver server/internal/wildencounter).
func parseLearnsets(root string, speciesIDs, moveIDs map[string]int) []SpeciesLearnset {
	pointerLines := readLines(filepath.Join(root, "src/data/pokemon/level_up_learnset_pointers.h"))
	pointerRe := regexp.MustCompile(`\[(SPECIES_\w+)\]\s*=\s*(\w+)`)
	arrayNameBySpecies := map[string]string{}
	for _, line := range pointerLines {
		if m := pointerRe.FindStringSubmatch(line); m != nil {
			arrayNameBySpecies[m[1]] = m[2]
		}
	}

	learnsetLines := readLines(filepath.Join(root, "src/data/pokemon/level_up_learnsets.h"))
	arrayStartRe := regexp.MustCompile(`^\s*static const u16 (\w+)\[\]\s*=\s*\{`)
	moveEntryRe := regexp.MustCompile(`LEVEL_UP_MOVE\(\s*(\d+)\s*,\s*(MOVE_\w+)\s*\)`)
	movesByArray := map[string][]LearnsetMove{}
	var currentArray string
	for _, line := range learnsetLines {
		if m := arrayStartRe.FindStringSubmatch(line); m != nil {
			currentArray = m[1]
			continue
		}
		if currentArray == "" {
			continue
		}
		if m := moveEntryRe.FindStringSubmatch(line); m != nil {
			level := atoi(m[1])
			if moveID, ok := moveIDs[m[2]]; ok {
				movesByArray[currentArray] = append(movesByArray[currentArray], LearnsetMove{Level: level, MoveID: moveID})
			}
		}
		if strings.Contains(line, "};") {
			currentArray = ""
		}
	}

	var out []SpeciesLearnset
	for macro, id := range speciesIDs {
		if id == 0 {
			continue
		}
		arrayName, ok := arrayNameBySpecies[macro]
		if !ok {
			continue
		}
		moves, ok := movesByArray[arrayName]
		if !ok {
			continue
		}
		out = append(out, SpeciesLearnset{SpeciesID: id, Moves: moves})
	}
	return out
}

// rawWildEncounters es la forma (parcial — solo lo que hace falta) de src/data/wild_encounters.json,
// que ya es JSON válido: no hace falta parsear C acá, a diferencia del resto de este archivo.
// rawEncounterSlot es la forma común de un slot de mons dentro de land_mons/water_mons/
// rock_smash_mons (fishing_mons tiene la misma forma pero se procesa aparte por los rod groups).
type rawEncounterSlot struct {
	MinLevel int    `json:"min_level"`
	MaxLevel int    `json:"max_level"`
	Species  string `json:"species"`
}

type rawMonsInfo struct {
	EncounterRate int                `json:"encounter_rate"`
	Mons          []rawEncounterSlot `json:"mons"`
}

type rawWildEncounters struct {
	WildEncounterGroups []struct {
		Fields []struct {
			Type           string `json:"type"`
			EncounterRates []int  `json:"encounter_rates"`
			// Groups: solo presente en el field "fishing_mons" — mapea cada caña (old_rod/
			// good_rod/super_rod) a los ÍNDICES (dentro de Mons/EncounterRates) que le
			// corresponden. Ver comentario de FishingSlot en el struct de arriba.
			Groups map[string][]int `json:"groups"`
		} `json:"fields"`
		Encounters []struct {
			Map           string       `json:"map"`
			LandMons      *rawMonsInfo `json:"land_mons"`
			WaterMons     *rawMonsInfo `json:"water_mons"`
			RockSmashMons *rawMonsInfo `json:"rock_smash_mons"`
			FishingMons   *rawMonsInfo `json:"fishing_mons"`
		} `json:"encounters"`
	} `json:"wild_encounter_groups"`
}

// parseEncounters lee land_mons/water_mons/rock_smash_mons/fishing_mons de cada mapa. Los pesos
// de encounter_rates son fijos y compartidos por todos los mapas para cada tipo (declarados una
// sola vez en "fields", no por mapa) — land_mons real de Gen3 usa la misma fórmula de tasa que
// water_mons/rock_smash_mons (ver WildEncounterCheck/RockSmashWildEncounter en
// src/wild_encounter.c, ambas llaman la misma función), así que se procesan igual acá.
func parseEncounters(path string, speciesIDs map[string]int) []MapEncounters {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "no se pudo leer wild_encounters.json: %v\n", err)
		os.Exit(1)
	}
	var raw rawWildEncounters
	if err := json.Unmarshal(data, &raw); err != nil {
		fmt.Fprintf(os.Stderr, "parseando wild_encounters.json: %v\n", err)
		os.Exit(1)
	}

	var landWeights, waterWeights, rockWeights, fishingWeights []int
	var fishingGroups map[string][]int
	for _, group := range raw.WildEncounterGroups {
		for _, f := range group.Fields {
			switch f.Type {
			case "land_mons":
				landWeights = f.EncounterRates
			case "water_mons":
				waterWeights = f.EncounterRates
			case "rock_smash_mons":
				rockWeights = f.EncounterRates
			case "fishing_mons":
				fishingWeights = f.EncounterRates
				fishingGroups = f.Groups
			}
		}
	}

	toSlots := func(mons []rawEncounterSlot, weights []int) []EncounterSlot {
		var slots []EncounterSlot
		for i, mon := range mons {
			speciesID, ok := speciesIDs[mon.Species]
			if !ok {
				continue
			}
			weight := 0
			if i < len(weights) {
				weight = weights[i]
			}
			slots = append(slots, EncounterSlot{SpeciesID: speciesID, MinLevel: mon.MinLevel, MaxLevel: mon.MaxLevel, Weight: weight})
		}
		return slots
	}

	// rodTierOf devuelve a qué caña pertenece el índice i dentro de fishing_mons.mons, buscando
	// en qué lista de fishingGroups aparece ese índice — O(1) en la práctica (3 grupos, <=10
	// índices en total).
	rodTierOf := func(i int) string {
		for tier, indices := range fishingGroups {
			for _, idx := range indices {
				if idx == i {
					return tier
				}
			}
		}
		return ""
	}

	var out []MapEncounters
	for _, group := range raw.WildEncounterGroups {
		for _, e := range group.Encounters {
			if e.LandMons == nil && e.WaterMons == nil && e.RockSmashMons == nil && e.FishingMons == nil {
				continue
			}
			me := MapEncounters{MapID: e.Map}
			if e.LandMons != nil {
				me.EncounterRate = e.LandMons.EncounterRate
				me.Slots = toSlots(e.LandMons.Mons, landWeights)
			}
			if e.WaterMons != nil {
				me.WaterEncounterRate = e.WaterMons.EncounterRate
				me.WaterSlots = toSlots(e.WaterMons.Mons, waterWeights)
			}
			if e.RockSmashMons != nil {
				me.RockSmashEncounterRate = e.RockSmashMons.EncounterRate
				me.RockSmashSlots = toSlots(e.RockSmashMons.Mons, rockWeights)
			}
			if e.FishingMons != nil {
				for i, mon := range e.FishingMons.Mons {
					speciesID, ok := speciesIDs[mon.Species]
					if !ok {
						continue
					}
					weight := 0
					if i < len(fishingWeights) {
						weight = fishingWeights[i]
					}
					me.FishingSlots = append(me.FishingSlots, FishingSlot{
						SpeciesID: speciesID, MinLevel: mon.MinLevel, MaxLevel: mon.MaxLevel,
						Weight: weight, RodTier: rodTierOf(i),
					})
				}
			}
			out = append(out, me)
		}
	}
	return out
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func readLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "no se pudo leer %s: %v\n", path, err)
		os.Exit(1)
	}
	return strings.Split(string(data), "\n")
}

func writeJSON(path string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "serializando %s: %v\n", path, err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "escribiendo %s: %v\n", path, err)
		os.Exit(1)
	}
}
