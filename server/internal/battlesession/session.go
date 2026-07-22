// Package battlesession orquesta una batalla PvP en vivo entre dos personajes conectados:
// desafío/aceptación, arma los Fighter iniciales (ver server/internal/battle, que solo sabe
// pelear, no de jugadores/DB) y resuelve turnos a medida que ambos lados mandan su acción.
//
// A diferencia de trade/market (sesiones en Postgres), el estado de una batalla vive en
// memoria (protegido por un mutex), igual que ws.Hub: una batalla es transitoria y se resuelve
// turno a turno con latencia baja, serializar cada Fighter a la base de datos en cada golpe no
// aporta nada (nadie necesita reanudar una batalla tras un restart del servidor). Lo único que
// se persiste es el HP final de cada Pokémon al terminar (ver pokemon.Service.UpdateHP).
package battlesession

import (
	"errors"
	"math/rand"
	"sync"

	"github.com/google/uuid"

	"pokemon-online/server/internal/battle"
	"pokemon-online/server/internal/pokemon"
)

var (
	ErrSessionNotFound  = errors.New("sesión de batalla no encontrada")
	ErrInvalidState     = errors.New("la sesión de batalla no está en el estado esperado")
	ErrNotParticipant   = errors.New("el jugador no pertenece a esta sesión de batalla")
	ErrActionAlreadySet = errors.New("ya mandaste tu acción para este turno, esperá al rival")
)

type status int

const (
	statusPending status = iota
	statusActive
	statusFinished
)

// fighterSlot agrupa lo que battlesession necesita de un lado de la pelea además del
// battle.Fighter puro: a qué personaje/Pokémon (fila real en la tabla `pokemon`) pertenece,
// para poder notificarlo y persistir su HP al terminar.
type fighterSlot struct {
	characterID string
	pokemonID   string
	fighter     *battle.Fighter
	pending     *battle.Action
}

type Session struct {
	ID     string
	status status
	sides  [2]fighterSlot
	rng    *rand.Rand
}

// PokemonView es el resumen de un Fighter para mandarle al cliente en battle_start (no
// battle.Fighter directo: el cliente no necesita PP-por-slot como array crudo ni Stages).
type PokemonView struct {
	PokemonID string
	Species   int
	Nickname  string
	Level     int
	CurrentHP int
	MaxHP     int
}

// TurnEvent es un battle.Event ya traducido a IDs de personaje (no índice 0/1 de Fighter,
// que no significa nada para el cliente) — quien orquesta el envío decide a quién avisar.
type TurnEvent struct {
	Type          battle.EventType
	ActorCharID   string
	MoveID        int
	Damage        int
	Effectiveness float64
	Fainted       bool
}

// TurnResult es lo que produce SubmitAction cuando ambos lados ya mandaron su acción y el
// turno se resolvió.
type TurnResult struct {
	Events        []TurnEvent
	HPByCharacter map[string]int // HP restante de cada lado tras el turno
	Finished      bool
	WinnerCharID  string // vacío si no terminó
	LoserCharID   string
}

type Service struct {
	mu       sync.Mutex
	sessions map[string]*Session
	pokemon  *pokemon.Service
}

func NewService(pokemonSvc *pokemon.Service) *Service {
	return &Service{sessions: make(map[string]*Session), pokemon: pokemonSvc}
}

// Challenge crea una sesión en estado "pending" — todavía no arma los Fighter (el rival puede
// declinar, o ni siquiera tener un Pokémon activo, sin que valga la pena consultar la DB antes
// de saber si va a aceptar).
func (s *Service) Challenge(charAID, charBID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionID := uuid.NewString()
	s.sessions[sessionID] = &Session{
		ID:     sessionID,
		status: statusPending,
		sides:  [2]fighterSlot{{characterID: charAID}, {characterID: charBID}},
	}
	return sessionID
}

// Accept arma los dos Fighter reales (consulta el Pokémon activo de cada lado) y pasa la
// sesión a "active". Devuelve el character_id y la vista de Pokémon de cada lado (0 = quien
// retó, ver Challenge) para que el Router arme el battle_start de cada perspectiva.
func (s *Service) Accept(sessionID string) (charA, charB string, a, b PokemonView, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return "", "", PokemonView{}, PokemonView{}, ErrSessionNotFound
	}
	if sess.status != statusPending {
		return "", "", PokemonView{}, PokemonView{}, ErrInvalidState
	}

	views := [2]PokemonView{}
	for i := range sess.sides {
		p, err := s.pokemon.GetActive(sess.sides[i].characterID)
		if err != nil {
			return "", "", PokemonView{}, PokemonView{}, err
		}
		type1, type2 := pokemon.SpeciesTypes(p.Species)
		sess.sides[i].pokemonID = p.ID
		sess.sides[i].fighter = battle.NewFighterFromPokemon(p, type1, type2)
		views[i] = PokemonView{
			PokemonID: p.ID, Species: p.Species, Nickname: p.Nickname,
			Level: p.Level, CurrentHP: p.CurrentHP, MaxHP: p.MaxHP,
		}
	}

	sess.status = statusActive
	sess.rng = rand.New(rand.NewSource(rand.Int63()))
	return sess.sides[0].characterID, sess.sides[1].characterID, views[0], views[1], nil
}

// Decline/Cancel: ver Cancel más abajo, comparten la misma lógica (una sesión pending o active
// se puede abortar en cualquier momento, ej. desconexión de un lado).
func (s *Service) Cancel(sessionID string) (charA, charB string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return "", "", ErrSessionNotFound
	}
	delete(s.sessions, sessionID)
	return sess.sides[0].characterID, sess.sides[1].characterID, nil
}

// CancelActiveForCharacter aborta toda sesión (pending o active) donde participe characterID —
// se usa al desconectarse, mismo patrón que trade.Service.CancelActiveForCharacter.
func (s *Service) CancelActiveForCharacter(characterID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var otherCharIDs []string
	for id, sess := range s.sessions {
		var other string
		var found bool
		if sess.sides[0].characterID == characterID {
			other, found = sess.sides[1].characterID, true
		} else if sess.sides[1].characterID == characterID {
			other, found = sess.sides[0].characterID, true
		}
		if found {
			delete(s.sessions, id)
			otherCharIDs = append(otherCharIDs, other)
		}
	}
	return otherCharIDs
}

// SubmitAction registra la acción de characterID para el turno actual. Si el rival ya había
// mandado la suya, resuelve el turno entero (battle.ResolveTurn) y devuelve el resultado; si
// no, devuelve (nil, nil) — falta el otro lado todavía.
func (s *Service) SubmitAction(sessionID, characterID string, moveSlot int) (*TurnResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if sess.status != statusActive {
		return nil, ErrInvalidState
	}

	selfIdx, ok := sideIndexOf(sess, characterID)
	if !ok {
		return nil, ErrNotParticipant
	}
	if sess.sides[selfIdx].pending != nil {
		return nil, ErrActionAlreadySet
	}
	sess.sides[selfIdx].pending = &battle.Action{MoveSlot: moveSlot}

	otherIdx := 1 - selfIdx
	if sess.sides[otherIdx].pending == nil {
		return nil, nil // falta el rival
	}

	fighters := [2]*battle.Fighter{sess.sides[0].fighter, sess.sides[1].fighter}
	actions := [2]battle.Action{*sess.sides[0].pending, *sess.sides[1].pending}
	rawEvents := battle.ResolveTurn(fighters, actions, sess.rng)
	sess.sides[0].pending, sess.sides[1].pending = nil, nil

	events := make([]TurnEvent, 0, len(rawEvents))
	for _, e := range rawEvents {
		events = append(events, TurnEvent{
			Type: e.Type, ActorCharID: sess.sides[e.FighterIdx].characterID,
			MoveID: e.MoveID, Damage: e.Damage, Effectiveness: e.Effectiveness, Fainted: e.Fainted,
		})
	}

	result := &TurnResult{
		Events: events,
		HPByCharacter: map[string]int{
			sess.sides[0].characterID: fighters[0].CurrentHP,
			sess.sides[1].characterID: fighters[1].CurrentHP,
		},
	}

	// Persistir el HP restante de ambos lados en cada turno (no solo al final): si un jugador
	// se desconecta a mitad de la pelea, su Pokémon no debe quedar con el HP de antes de empezar.
	for _, side := range sess.sides {
		_ = s.pokemon.UpdateHP(side.pokemonID, sideCurrentHP(side))
	}

	if fighters[0].CurrentHP <= 0 || fighters[1].CurrentHP <= 0 {
		sess.status = statusFinished
		result.Finished = true
		if fighters[0].CurrentHP <= 0 {
			result.WinnerCharID, result.LoserCharID = sess.sides[1].characterID, sess.sides[0].characterID
		} else {
			result.WinnerCharID, result.LoserCharID = sess.sides[0].characterID, sess.sides[1].characterID
		}
		delete(s.sessions, sessionID)
	}

	return result, nil
}

func sideIndexOf(sess *Session, characterID string) (int, bool) {
	if sess.sides[0].characterID == characterID {
		return 0, true
	}
	if sess.sides[1].characterID == characterID {
		return 1, true
	}
	return -1, false
}

func sideCurrentHP(side fighterSlot) int {
	if side.fighter == nil {
		return 0
	}
	return side.fighter.CurrentHP
}
