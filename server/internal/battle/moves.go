package battle

import (
	"encoding/json"
	"fmt"
	"os"
)

// Legacy: los 5 movimientos verificados a mano antes de tener el catálogo completo (ver
// server/cmd/gendata) — siguen siendo válidos como constantes con nombre, ahora respaldados
// por el catálogo generado en vez de un mapa literal de 5 entradas.
const (
	MovePound   = 1
	MoveScratch = 10
	MoveTackle  = 33
	MoveLeer    = 43
	MoveGrowl   = 45
)

// Stat identifica cuál de las 5 estadísticas modificables por combate (no Accuracy/Evasion
// todavía, ver Effect) cambia un movimiento de estado puro.
type Stat int

const (
	StatAttack Stat = iota
	StatDefense
	StatSpAttack
	StatSpDefense
	StatSpeed
)

// Effect agrupa en qué categoría mecánica cae un movimiento para este motor:
//   - EffectHit: hace daño con la fórmula real (power > 0) — incluye movimientos con efectos
//     secundarios reales en Gen3 (quemar/paralizar/retroceder/etc.) que TODAVÍA no se simulan;
//     se resuelve como golpe normal, ignorando el efecto secundario (limitación real, no bug
//     oculto — documentado también en server/cmd/gendata/main.go).
//   - EffectStatChange: movimiento de estado puro (power == 0) que sube o baja una de las 5
//     estadísticas en ±1/±2 escalones, a sí mismo o al rival según TargetsSelf.
//   - EffectStatusOther: cualquier otro efecto de estado (dormir, quemar, clima, drenar,
//     proteger, daño fijo, OHKO, multi-golpe, Accuracy/Evasion, etc.) — Gen3 tiene ~150 efectos
//     distintos y implementarlos todos es un motor de batalla completo aparte; se resuelve sin
//     efecto mecánico (consume el turno/PP, no rompe nada) hasta que se amplíe.
type Effect int

const (
	EffectHit Effect = iota
	EffectStatChange
	EffectStatusOther
)

type Move struct {
	ID       int
	Name     string
	Power    int
	Type     int
	Accuracy int // 0-100; 0 es un valor especial de pokeemerald que significa "nunca falla", NO 0% (ver ResolveTurn)
	PP       int
	Effect   Effect

	// Válidos solo si Effect == EffectStatChange.
	StatChange  Stat
	StatDelta   int // -2, -1, +1 o +2
	TargetsSelf bool
}

// statChangeEffects mapea el ID crudo de EFFECT_* de pokeemerald (ver
// include/constants/battle_move_effects.h, capturado tal cual por server/cmd/gendata) a qué
// estadística cambia y en cuánto — solo la familia "sube/baja N escalones sin dañar";
// Accuracy/Evasion (tienen una tabla de multiplicador distinta a las otras 5, y afectan el
// cálculo de precisión en vez del daño) y las variantes "_HIT" (dañan Y además cambian una
// estadística con cierta probabilidad) quedan fuera deliberadamente — cualquier movimiento con
// uno de esos efectos cae en EffectStatusOther si no hace daño, o EffectHit si hace daño.
var statChangeEffects = map[int]struct {
	Stat  Stat
	Delta int
}{
	10: {StatAttack, 1}, 11: {StatDefense, 1}, 12: {StatSpeed, 1}, 13: {StatSpAttack, 1}, 14: {StatSpDefense, 1},
	18: {StatAttack, -1}, 19: {StatDefense, -1}, 20: {StatSpeed, -1}, 21: {StatSpAttack, -1}, 22: {StatSpDefense, -1},
	50: {StatAttack, 2}, 51: {StatDefense, 2}, 52: {StatSpeed, 2}, 53: {StatSpAttack, 2}, 54: {StatSpDefense, 2},
	58: {StatAttack, -2}, 59: {StatDefense, -2}, 60: {StatSpeed, -2}, 61: {StatSpAttack, -2}, 62: {StatSpDefense, -2},
}

var moves map[int]Move

// rawMoveEntry es la forma exacta del JSON que emite server/cmd/gendata (data/pokemon/moves.json).
type rawMoveEntry struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Type        int    `json:"type"`
	Power       int    `json:"power"`
	Accuracy    int    `json:"accuracy"`
	PP          int    `json:"pp"`
	Effect      int    `json:"effect"`
	TargetsSelf bool   `json:"targets_self"`
}

// LoadMoveCatalog carga el catálogo completo de movimientos (~354) desde el JSON generado por
// `go run ./cmd/gendata` a partir del código fuente real de pokeemerald — se llama una sola vez
// al arrancar el servidor (ver main.go), antes de que cualquier batalla pueda empezar.
func LoadMoveCatalog(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("leyendo catálogo de movimientos (%s): %w", path, err)
	}
	var raw []rawMoveEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parseando catálogo de movimientos: %w", err)
	}

	loaded := make(map[int]Move, len(raw))
	for _, r := range raw {
		m := Move{
			ID: r.ID, Name: r.Name, Power: r.Power, Type: r.Type, Accuracy: r.Accuracy, PP: r.PP,
			TargetsSelf: r.TargetsSelf,
		}
		switch {
		case r.Power > 0:
			m.Effect = EffectHit
		default:
			if sc, ok := statChangeEffects[r.Effect]; ok {
				m.Effect = EffectStatChange
				m.StatChange, m.StatDelta = sc.Stat, sc.Delta
			} else {
				m.Effect = EffectStatusOther
			}
		}
		loaded[r.ID] = m
	}
	moves = loaded
	return nil
}

func MoveByID(id int) (Move, bool) {
	m, ok := moves[id]
	return m, ok
}

// IsPhysical: en Gen 1-3 (a diferencia de Gen 4+) la categoría física/especial depende del TIPO
// del movimiento, no del movimiento en sí — regla real de pokeemerald (IS_TYPE_PHYSICAL).
func IsPhysical(moveType int) bool {
	switch moveType {
	case TypeNormal, TypeFighting, TypeFlying, TypeGround, TypeRock, TypeBug, TypeGhost, TypePoison, TypeSteel:
		return true
	default:
		return false
	}
}
