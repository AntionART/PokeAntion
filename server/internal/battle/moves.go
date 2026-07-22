package battle

// Move IDs de include/constants/moves.h, power/type/accuracy/pp de src/data/battle_moves.h —
// solo los 5 movimientos que los 3 iniciales conocen a nivel 5 (ver pokemon.starterMoves). Se
// amplía este catálogo a medida que el equipo/movimientos disponibles crezcan (subir de nivel,
// MTs, etc. — todavía no implementado); no tiene sentido cargar los ~350 movimientos del juego
// antes de necesitarlos.
const (
	MovePound   = 1
	MoveScratch = 10
	MoveTackle  = 33
	MoveLeer    = 43
	MoveGrowl   = 45
)

// Effect agrupa los pocos efectos que soportamos hoy: HIT (daño puro), o una bajada de stat de
// un escalón sin daño (Growl/Leer). El motor real de pokeemerald tiene ~150 efectos distintos
// (EFFECT_HIT, EFFECT_DEFENSE_DOWN, etc.) — implementamos los que hacen falta para las 5
// movidas soportadas, no una tabla completa que no se usaría todavía.
type Effect int

const (
	EffectHit Effect = iota
	EffectAttackDown
	EffectDefenseDown
)

type Move struct {
	ID       int
	Power    int
	Type     int
	Accuracy int // 0-100
	PP       int
	Effect   Effect
}

var moves = map[int]Move{
	MovePound:   {ID: MovePound, Power: 40, Type: TypeNormal, Accuracy: 100, PP: 35, Effect: EffectHit},
	MoveScratch: {ID: MoveScratch, Power: 40, Type: TypeNormal, Accuracy: 100, PP: 35, Effect: EffectHit},
	MoveTackle:  {ID: MoveTackle, Power: 35, Type: TypeNormal, Accuracy: 95, PP: 35, Effect: EffectHit},
	MoveLeer:    {ID: MoveLeer, Power: 0, Type: TypeNormal, Accuracy: 100, PP: 30, Effect: EffectDefenseDown},
	MoveGrowl:   {ID: MoveGrowl, Power: 0, Type: TypeNormal, Accuracy: 100, PP: 40, Effect: EffectAttackDown},
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
