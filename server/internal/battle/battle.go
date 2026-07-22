package battle

import (
	"math/rand"

	"pokemon-online/server/internal/pokemon"
)

// NewFighterFromPokemon arma un Fighter listo para pelear a partir de un registro persistido
// (ver pokemon.Service.GetParty) — este paquete no sabe leer la base de datos, solo pelear;
// species1/species2 salen de pokemon.SpeciesTypes(p.Species) (ese paquete SÍ conoce los tipos).
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

// StatStages son los escalones -6..+6 de modificador temporal de stat DURANTE el combate (no
// persisten al terminar) — solo Attack/Defense hoy porque son los únicos que Growl/Leer tocan;
// se amplía cuando haya movimientos que afecten otros stats.
type StatStages struct {
	Attack, Defense int
}

// stageMultiplier: tabla real de Gen3 (stage -6 = x2/8 ... 0 = x1 ... +6 = x8/2).
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
}

// ResolveTurn aplica las acciones de ambos combatientes en orden de Velocidad (el más rápido
// primero; empate resuelto al azar, igual que el juego real) y devuelve el log de lo que pasó.
// Muta fighters[i].CurrentHP/PP/Stages in-place — quien llama es responsable de persistir el
// resultado (ver pokemon.Service) si corresponde.
func ResolveTurn(fighters [2]*Fighter, actions [2]Action, rng *rand.Rand) []Event {
	order := [2]int{0, 1}
	if fighters[1].Speed > fighters[0].Speed || (fighters[1].Speed == fighters[0].Speed && rng.Intn(2) == 0) {
		order = [2]int{1, 0}
	}

	var events []Event
	for _, actorIdx := range order {
		actor := fighters[actorIdx]
		target := fighters[1-actorIdx]
		if actor.CurrentHP <= 0 || target.CurrentHP <= 0 {
			continue // no puede actuar desmayado, o el rival ya cayó en la acción anterior
		}

		slot := actions[actorIdx].MoveSlot
		moveID := actor.Moves[slot]
		move, ok := MoveByID(moveID)
		if !ok {
			continue
		}
		if actor.PP[slot] <= 0 {
			events = append(events, Event{Type: EventNoPP, FighterIdx: actorIdx, MoveID: moveID})
			continue
		}
		actor.PP[slot]--

		if rng.Float64()*100 >= float64(move.Accuracy) {
			events = append(events, Event{Type: EventMiss, FighterIdx: actorIdx, MoveID: moveID})
			continue
		}

		switch move.Effect {
		case EffectHit:
			isCrit := rng.Intn(16) == 0 // 1/16 — probabilidad base real de golpe crítico en Gen3
			randRoll := 0.85 + rng.Float64()*0.15
			atkStats := Combatant{Level: actor.Level, Attack: actor.effectiveAttack(), SpAttack: actor.SpAttack, Type1: actor.Type1, Type2: actor.Type2}
			defStats := Combatant{Defense: target.effectiveDefense(), SpDefense: target.SpDefense, Type1: target.Type1, Type2: target.Type2}
			dmg := CalculateDamage(atkStats, defStats, move, isCrit, randRoll)
			target.CurrentHP -= dmg
			fainted := false
			if target.CurrentHP <= 0 {
				target.CurrentHP = 0
				fainted = true
			}
			events = append(events, Event{
				Type: EventDamage, FighterIdx: actorIdx, MoveID: moveID, Damage: dmg,
				Effectiveness: Effectiveness(move.Type, target.Type1, target.Type2), Fainted: fainted,
			})
			if fainted {
				events = append(events, Event{Type: EventFaint, FighterIdx: 1 - actorIdx})
			}
		case EffectAttackDown:
			if target.Stages.Attack > -6 {
				target.Stages.Attack--
			}
			events = append(events, Event{Type: EventStatChange, FighterIdx: 1 - actorIdx, MoveID: moveID})
		case EffectDefenseDown:
			if target.Stages.Defense > -6 {
				target.Stages.Defense--
			}
			events = append(events, Event{Type: EventStatChange, FighterIdx: 1 - actorIdx, MoveID: moveID})
		}
	}
	return events
}
