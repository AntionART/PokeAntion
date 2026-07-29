package battle

import (
	"math/rand"

	"pokemon-online/server/internal/pokemon"
)

// NewFighterFromPokemon arma un Fighter listo para pelear a partir de un registro persistido
// (ver pokemon.Service.GetParty) — este paquete no sabe leer la base de datos, solo pelear;
// type1/type2 salen del catálogo de especies (ver server/internal/pokemon.SpeciesTypes, que a
// su vez usa el catálogo cargado por pokemon.LoadSpeciesCatalog).
func NewFighterFromPokemon(p pokemon.Pokemon, type1, type2 int) *Fighter {
	return &Fighter{
		Combatant: Combatant{
			Level: p.Level, Attack: p.Attack, Defense: p.Defense,
			SpAttack: p.SpAttack, SpDefense: p.SpDefense, Speed: p.Speed,
			Type1: type1, Type2: type2,
		},
		CurrentHP: p.CurrentHP, MaxHP: p.MaxHP,
		Moves: p.MoveIDs(), PP: p.PPs(),
	}
}

// StatStages son los escalones -6..+6 de modificador temporal DURANTE el combate (no persisten
// al terminar) de las 5 estadísticas que el catálogo completo de movimientos puede subir/bajar
// (ver moves.go statChangeEffects) — Accuracy/Evasion usan una tabla de multiplicador distinta
// en Gen3 y afectan la precisión en vez de estas 5, quedan fuera deliberadamente por ahora.
type StatStages struct {
	Attack, Defense, SpAttack, SpDefense, Speed int
}

// stageMultiplier: tabla real de Gen3 (stage -6 = x2/8 ... 0 = x1 ... +6 = x8/2), la misma
// fórmula para las 5 estadísticas de StatStages (Accuracy/Evasion tienen la suya propia, no
// implementada todavía).
func stageMultiplier(stage int) float64 {
	if stage > 6 {
		stage = 6
	}
	if stage < -6 {
		stage = -6
	}
	if stage >= 0 {
		return float64(2+stage) / 2
	}
	return 2 / float64(2-stage)
}

// clampStage mantiene un escalón dentro de -6..+6 tras sumarle delta — un movimiento que ya
// esté en el tope simplemente no tiene efecto adicional (regla real del juego).
func clampStage(current, delta int) int {
	n := current + delta
	if n > 6 {
		return 6
	}
	if n < -6 {
		return -6
	}
	return n
}

// Fighter es un combatiente durante UNA batalla — envuelve los stats base (llegan ya
// calculados desde pokemon.Pokemon, ver server/internal/pokemon) más el estado transitorio del
// combate (HP actual, stages, PP restante), que este paquete no persiste: quien orquesta la
// batalla decide qué guardar en la tabla `pokemon` al terminar (HP restante, nada más por ahora).
type Fighter struct {
	Combatant
	CurrentHP int
	MaxHP     int
	Stages    StatStages
	Moves     [4]int
	PP        [4]int
}

func (f *Fighter) effectiveAttack() int {
	return int(float64(f.Attack) * stageMultiplier(f.Stages.Attack))
}
func (f *Fighter) effectiveDefense() int {
	return int(float64(f.Defense) * stageMultiplier(f.Stages.Defense))
}
func (f *Fighter) effectiveSpAttack() int {
	return int(float64(f.SpAttack) * stageMultiplier(f.Stages.SpAttack))
}
func (f *Fighter) effectiveSpDefense() int {
	return int(float64(f.SpDefense) * stageMultiplier(f.Stages.SpDefense))
}
func (f *Fighter) effectiveSpeed() int {
	return int(float64(f.Speed) * stageMultiplier(f.Stages.Speed))
}

// statPointer devuelve un puntero al escalón correspondiente en Stages, para que ResolveTurn
// pueda aplicar el cambio de CUALQUIERA de las 5 estadísticas con el mismo código (en vez de un
// switch repetido por estadística, ver el switch histórico de solo Attack/Defense que esto
// reemplaza).
func (s *StatStages) statPointer(stat Stat) *int {
	switch stat {
	case StatAttack:
		return &s.Attack
	case StatDefense:
		return &s.Defense
	case StatSpAttack:
		return &s.SpAttack
	case StatSpDefense:
		return &s.SpDefense
	default: // StatSpeed
		return &s.Speed
	}
}

type Action struct {
	MoveSlot int // índice 0-3 en Fighter.Moves
}

type EventType int

const (
	EventDamage EventType = iota
	EventMiss
	EventFaint
	EventStatChange
	EventNoPP
	// EventNoEffect: movimiento de estado real de Gen3 sin mecánica implementada todavía (ver
	// EffectStatusOther en moves.go) — se gasta el turno/PP pero no pasa nada visible, y se
	// avisa así en vez de fingir un resultado.
	EventNoEffect
	// EventSwitch/EventItemUsed los emite battlesession (no este paquete: un Fighter no sabe de
	// especies/apodos/objetos, ver battlesession.TurnEvent) cuando un lado cambia de Pokémon o
	// usa un objeto de curación — se listan acá para que EventType.String() cubra todos los
	// tipos que de verdad viajan por el protocolo.
	EventSwitch
	EventItemUsed
)

// String da el nombre estable que viaja por el protocolo (ver protocol.BattleEventPayload) —
// no el valor numérico crudo del iota, que rompería si se reordena esta lista.
func (t EventType) String() string {
	switch t {
	case EventDamage:
		return "damage"
	case EventMiss:
		return "miss"
	case EventFaint:
		return "faint"
	case EventStatChange:
		return "stat_change"
	case EventNoPP:
		return "no_pp"
	case EventNoEffect:
		return "no_effect"
	case EventSwitch:
		return "switch"
	case EventItemUsed:
		return "item_used"
	default:
		return "unknown"
	}
}

// Event es un paso del log de la resolución de un turno — el cliente lo consume para animar
// (mostrar "It was super effective!", bajar la barra de HP, etc.), no se le manda al cliente
// el resultado final crudo sin explicar cómo se llegó a él.
type Event struct {
	Type          EventType
	FighterIdx    int // 0 o 1: quién actuó
	MoveID        int
	Damage        int
	Effectiveness float64
	Fainted       bool
	// Amount es el cambio de escalón con signo (+1/+2 sube, -1/-2 baja) — solo para
	// EventStatChange; el objetivo real (self vs rival) ya se refleja en qué FighterIdx quedó
	// afectado, no en este campo.
	Amount int
}

// ResolveTurn aplica las acciones de ambos combatientes en orden de Velocidad EFECTIVA (con
// stages aplicados — el más rápido primero; empate resuelto al azar, igual que el juego real) y
// devuelve el log de lo que pasó. Muta fighters[i].CurrentHP/PP/Stages in-place — quien llama es
// responsable de persistir el resultado (ver pokemon.Service) si corresponde.
func ResolveTurn(fighters [2]*Fighter, actions [2]Action, rng *rand.Rand) []Event {
	order := [2]int{0, 1}
	if fighters[1].effectiveSpeed() > fighters[0].effectiveSpeed() ||
		(fighters[1].effectiveSpeed() == fighters[0].effectiveSpeed() && rng.Intn(2) == 0) {
		order = [2]int{1, 0}
	}

	var events []Event
	for _, actorIdx := range order {
		if fighters[actorIdx].CurrentHP <= 0 || fighters[1-actorIdx].CurrentHP <= 0 {
			continue // no puede actuar desmayado, o el rival ya cayó en la acción anterior
		}
		events = append(events, resolveAction(fighters, actorIdx, actions[actorIdx], rng)...)
	}
	return events
}

// ResolveSingleAction resuelve la acción de UN solo lado — para el caso en que el rival cambió
// de Pokémon este turno (ver battlesession): cambiar de Pokémon consume el turno entero (no se
// ataca Y se cambia a la vez, regla real de Gen3), así que solo el lado que sí atacó actúa,
// contra el Fighter que quedó activo del otro lado tras el cambio.
func ResolveSingleAction(fighters [2]*Fighter, actorIdx int, action Action, rng *rand.Rand) []Event {
	if fighters[actorIdx].CurrentHP <= 0 || fighters[1-actorIdx].CurrentHP <= 0 {
		return nil
	}
	return resolveAction(fighters, actorIdx, action, rng)
}

func resolveAction(fighters [2]*Fighter, actorIdx int, action Action, rng *rand.Rand) []Event {
	actor := fighters[actorIdx]
	target := fighters[1-actorIdx]

	slot := action.MoveSlot
	moveID := actor.Moves[slot]
	move, ok := MoveByID(moveID)
	if !ok {
		return nil
	}
	if actor.PP[slot] <= 0 {
		return []Event{{Type: EventNoPP, FighterIdx: actorIdx, MoveID: moveID}}
	}
	actor.PP[slot]--

	// Accuracy == 0 es un valor especial real de pokeemerald: "este movimiento nunca falla"
	// (Swords Dance, Amnesia, etc.), NO "0% de probabilidad de acertar" — sin este caso
	// especial, todo movimiento de estado con accuracy=0 fallaría siempre (bug real encontrado
	// al ampliar del catálogo de 5 a ~354 movimientos: con solo Growl/Leer, ambos accuracy=100,
	// nunca se había ejercitado este camino).
	if move.Accuracy > 0 && rng.Float64()*100 >= float64(move.Accuracy) {
		return []Event{{Type: EventMiss, FighterIdx: actorIdx, MoveID: moveID}}
	}

	switch move.Effect {
	case EffectHit:
		isCrit := rng.Intn(16) == 0 // 1/16 — probabilidad base real de golpe crítico en Gen3
		randRoll := 0.85 + rng.Float64()*0.15
		atkStats := Combatant{Level: actor.Level, Attack: actor.effectiveAttack(), SpAttack: actor.effectiveSpAttack(), Type1: actor.Type1, Type2: actor.Type2}
		defStats := Combatant{Defense: target.effectiveDefense(), SpDefense: target.effectiveSpDefense(), Type1: target.Type1, Type2: target.Type2}
		dmg := CalculateDamage(atkStats, defStats, move, isCrit, randRoll)
		target.CurrentHP -= dmg
		fainted := false
		if target.CurrentHP <= 0 {
			target.CurrentHP = 0
			fainted = true
		}
		events := []Event{{
			Type: EventDamage, FighterIdx: actorIdx, MoveID: moveID, Damage: dmg,
			Effectiveness: Effectiveness(move.Type, target.Type1, target.Type2), Fainted: fainted,
		}}
		if fainted {
			events = append(events, Event{Type: EventFaint, FighterIdx: 1 - actorIdx})
		}
		return events

	case EffectStatChange:
		affectedIdx := actorIdx
		if !move.TargetsSelf {
			affectedIdx = 1 - actorIdx
		}
		stagePtr := fighters[affectedIdx].Stages.statPointer(move.StatChange)
		*stagePtr = clampStage(*stagePtr, move.StatDelta)
		return []Event{{Type: EventStatChange, FighterIdx: affectedIdx, MoveID: moveID, Amount: move.StatDelta}}

	default: // EffectStatusOther
		return []Event{{Type: EventNoEffect, FighterIdx: actorIdx, MoveID: moveID}}
	}
}
