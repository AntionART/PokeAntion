// Package battlesession orquesta una batalla PvP en vivo entre dos personajes conectados:
// desafío/aceptación, arma los Fighter de TODO el equipo de cada lado (ver server/internal/
// battle, que solo sabe pelear, no de jugadores/DB) y resuelve turnos/cambios/huidas a medida
// que ambos lados mandan su acción.
//
// A diferencia de trade/market (sesiones en Postgres), el estado de una batalla vive en
// memoria (protegido por un mutex), igual que ws.Hub: una batalla es transitoria y se resuelve
// turno a turno con latencia baja, serializar cada Fighter a la base de datos en cada golpe no
// aporta nada (nadie necesita reanudar una batalla tras un restart del servidor). Lo único que
// se persiste es el HP de cada Pokémon del equipo al final de cada intercambio (ver
// pokemon.Service.UpdateHP).
package battlesession

import (
	"errors"
	"math/rand"
	"sync"

	"github.com/google/uuid"

	"pokemon-online/server/internal/battle"
	"pokemon-online/server/internal/inventory"
	"pokemon-online/server/internal/pokemon"
)

var (
	ErrSessionNotFound  = errors.New("sesión de batalla no encontrada")
	ErrInvalidState     = errors.New("la sesión de batalla no está en el estado esperado")
	ErrNotParticipant   = errors.New("el jugador no pertenece a esta sesión de batalla")
	ErrActionAlreadySet = errors.New("ya mandaste tu acción para este turno, esperá al rival")
	ErrMustSwitch       = errors.New("tu Pokémon activo se debilitó: tenés que elegir un reemplazo antes de atacar")
	ErrInvalidSwitch    = errors.New("no se puede cambiar a ese Pokémon (no existe, ya está activo, o está debilitado)")
	ErrNoAlivePokemon   = errors.New("no tenés ningún Pokémon en condiciones de pelear")
	ErrInvalidItem      = errors.New("objeto no válido para ese objetivo (curación en un Pokémon debilitado, revivir en uno con vida, o el objeto no existe)")
)

type status int

const (
	statusPending status = iota
	statusActive
	statusFinished
)

type actionKind int

const (
	actionMove actionKind = iota
	actionSwitch
	actionItem
)

type actionInput struct {
	kind     actionKind
	moveSlot int
	teamSlot int // índice en fighters/pokemonIDs (no team_slot de la DB necesariamente, ver Accept) — switch: a quién cambiar; item: a quién se le aplica
	itemID   int // solo actionItem
}

// ActionRequest es lo que Router traduce de battle_action/battle_switch/battle_item —
// SubmitAction acepta una sola forma unificada porque atacar, cambiar de Pokémon y usar un
// objeto comparten el mismo mecanismo de "esperar a que ambos lados manden su jugada de este
// intercambio" (ver resolveExchange).
type ActionRequest struct {
	IsSwitch bool
	IsItem   bool
	MoveSlot int
	TeamSlot int // índice 0-based dentro del equipo devuelto por Team() — a quién cambiar (IsSwitch) o a quién curar/revivir (IsItem)
	ItemID   int // solo si IsItem
}

// fighterSlot agrupa lo que battlesession necesita de un lado de la pelea además de los
// battle.Fighter puros: a qué personaje pertenecen, y para cada miembro del equipo (mismo
// orden/índice que fighters) su ID real en la tabla `pokemon`, especie y apodo — battle.Fighter
// no guarda nada de eso (solo pelea, no sabe de especies/nombres, ver server/internal/battle).
type fighterSlot struct {
	characterID string
	pokemonIDs  []string
	species     []int
	nicknames   []string
	fighters    []*battle.Fighter
	activeIdx   int
	pending     *actionInput
	needsSwitch bool // el activo se debilitó: este lado DEBE mandar un switch antes de que el intercambio siguiente se resuelva
}

type Session struct {
	ID     string
	status status
	sides  [2]fighterSlot
	rng    *rand.Rand
}

// PokemonView es el resumen de un Fighter para mandarle al cliente (battle_start, Team) — no
// battle.Fighter directo: el cliente no necesita PP-por-slot como array crudo ni Stages.
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
	Amount        int
	// TargetSpecies/TargetNickname se llenan solo para eventos de cambio de Pokémon o de uso de
	// objeto — battle.Event no sabe de especies/apodos/objetos (vive en server/internal/battle,
	// sin acceso a datos de Pokémon ni de inventario), así que ese dato lo agrega esta capa al
	// traducir. ItemID solo para EventItemUsed.
	TargetSpecies  int
	TargetNickname string
	ItemID         int
}

// TurnResult es lo que produce SubmitAction/Flee cuando el intercambio se resolvió.
type TurnResult struct {
	Events        []TurnEvent
	HPByCharacter map[string]int // HP restante del Pokémon ACTIVO de cada lado tras el intercambio
	// NeedsSwitch: character_id de quien tiene que mandar un battle_switch antes de que el
	// próximo intercambio pueda resolverse (su activo se debilitó y le queda equipo vivo).
	NeedsSwitch  string
	Finished     bool
	WinnerCharID string // vacío si no terminó
	LoserCharID  string
	Reason       string // "victory" (fin normal) o "fled" (ver Flee)
}

type Service struct {
	mu        sync.Mutex
	sessions  map[string]*Session
	pokemon   *pokemon.Service
	inventory *inventory.Service
}

func NewService(pokemonSvc *pokemon.Service, inventorySvc *inventory.Service) *Service {
	return &Service{sessions: make(map[string]*Session), pokemon: pokemonSvc, inventory: inventorySvc}
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

// Accept arma el equipo completo de Fighters de cada lado (todo team_slot en location='team')
// y pasa la sesión a "active". Devuelve el character_id y la vista del Pokémon ACTIVO de cada
// lado (0 = quien retó, ver Challenge) para que el Router arme el battle_start de cada
// perspectiva — el resto del equipo se pide aparte con Team() cuando el jugador abre el menú
// de cambiar de Pokémon (no hace falta mandarlo por adelantado si nunca lo abre).
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
		team, err := s.pokemon.GetParty(sess.sides[i].characterID)
		if err != nil {
			return "", "", PokemonView{}, PokemonView{}, err
		}
		if len(team) == 0 {
			return "", "", PokemonView{}, PokemonView{}, pokemon.ErrNoActivePokemon
		}

		activeIdx := -1
		fighters := make([]*battle.Fighter, len(team))
		pokemonIDs := make([]string, len(team))
		species := make([]int, len(team))
		nicknames := make([]string, len(team))
		for j, p := range team {
			type1, type2 := pokemon.SpeciesTypes(p.Species)
			fighters[j] = battle.NewFighterFromPokemon(p, type1, type2)
			pokemonIDs[j] = p.ID
			species[j] = p.Species
			nicknames[j] = p.Nickname
			if activeIdx == -1 && p.CurrentHP > 0 {
				activeIdx = j
			}
		}
		if activeIdx == -1 {
			return "", "", PokemonView{}, PokemonView{}, ErrNoAlivePokemon
		}

		sess.sides[i].fighters = fighters
		sess.sides[i].pokemonIDs = pokemonIDs
		sess.sides[i].species = species
		sess.sides[i].nicknames = nicknames
		sess.sides[i].activeIdx = activeIdx
		views[i] = pokemonViewOf(team[activeIdx])
	}

	sess.status = statusActive
	sess.rng = rand.New(rand.NewSource(rand.Int63()))
	return sess.sides[0].characterID, sess.sides[1].characterID, views[0], views[1], nil
}

func pokemonViewOf(p pokemon.Pokemon) PokemonView {
	return PokemonView{
		PokemonID: p.ID, Species: p.Species, Nickname: p.Nickname,
		Level: p.Level, CurrentHP: p.CurrentHP, MaxHP: p.MaxHP,
	}
}

// Team devuelve el equipo completo de characterID en la sesión (para el menú "Pokémon" del
// cliente al elegir a quién cambiar) junto con el índice del que está activo ahora mismo.
func (s *Service) Team(sessionID, characterID string) (team []PokemonView, activeIdx int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, 0, ErrSessionNotFound
	}
	idx, ok := sideIndexOf(sess, characterID)
	if !ok {
		return nil, 0, ErrNotParticipant
	}
	side := sess.sides[idx]
	views := make([]PokemonView, len(side.fighters))
	for i, f := range side.fighters {
		views[i] = PokemonView{
			PokemonID: side.pokemonIDs[i], Species: side.species[i], Nickname: side.nicknames[i],
			Level: f.Level, CurrentHP: f.CurrentHP, MaxHP: f.MaxHP,
		}
	}
	return views, side.activeIdx, nil
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

// Flee termina la batalla de inmediato a favor del rival — a diferencia de un movimiento o un
// cambio de Pokémon, huir NO espera a que el rival también mande su jugada (en el juego real,
// intentar huir de un entrenador ni siquiera es una opción; acá se simplifica a "huir de una
// batalla PvP siempre funciona y cuenta como rendirse", documentado como tal en el protocolo).
func (s *Service) Flee(sessionID, characterID string) (winner, loser string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return "", "", ErrSessionNotFound
	}
	if sess.status != statusActive {
		return "", "", ErrInvalidState
	}
	idx, ok := sideIndexOf(sess, characterID)
	if !ok {
		return "", "", ErrNotParticipant
	}

	otherIdx := 1 - idx
	winner, loser = sess.sides[otherIdx].characterID, sess.sides[idx].characterID
	delete(s.sessions, sessionID)
	return winner, loser, nil
}

// SubmitAction registra la jugada de characterID para el intercambio actual (atacar o cambiar
// de Pokémon). Si el rival ya había mandado la suya, resuelve el intercambio entero y devuelve
// el resultado; si no, devuelve (nil, nil) — falta el otro lado todavía.
func (s *Service) SubmitAction(sessionID, characterID string, req ActionRequest) (*TurnResult, error) {
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
	side := &sess.sides[selfIdx]
	if side.pending != nil {
		return nil, ErrActionAlreadySet
	}
	if side.needsSwitch && !req.IsSwitch {
		return nil, ErrMustSwitch
	}

	switch {
	case req.IsSwitch:
		if req.TeamSlot < 0 || req.TeamSlot >= len(side.fighters) ||
			req.TeamSlot == side.activeIdx || side.fighters[req.TeamSlot].CurrentHP <= 0 {
			return nil, ErrInvalidSwitch
		}
		side.pending = &actionInput{kind: actionSwitch, teamSlot: req.TeamSlot}

	case req.IsItem:
		info, ok := inventory.Catalog[req.ItemID]
		// Las Poké Ball no tienen sentido acá: no se puede atrapar al Pokémon de otro jugador
		// (eso solo existe contra un Pokémon salvaje, ver server/internal/wildencounter).
		if !ok || info.Effect == inventory.EffectBall || req.TeamSlot < 0 || req.TeamSlot >= len(side.fighters) {
			return nil, ErrInvalidItem
		}
		targetHP := side.fighters[req.TeamSlot].CurrentHP
		wantsFainted := info.Effect == inventory.EffectRevive || info.Effect == inventory.EffectReviveFull
		if wantsFainted == (targetHP > 0) {
			return nil, ErrInvalidItem // curación en un debilitado, o revivir en uno con vida
		}
		// Consumir el inventario acá (no al resolver): si no tiene el objeto, se rechaza de
		// entrada — nunca se llega a gastar el turno del jugador en un objeto que no tenía.
		if err := s.inventory.Consume(characterID, req.ItemID); err != nil {
			return nil, err
		}
		side.pending = &actionInput{kind: actionItem, teamSlot: req.TeamSlot, itemID: req.ItemID}

	default:
		side.pending = &actionInput{kind: actionMove, moveSlot: req.MoveSlot}
	}

	otherIdx := 1 - selfIdx
	if sess.sides[otherIdx].pending == nil {
		return nil, nil // falta el rival
	}

	return s.resolveExchange(sess), nil
}

// resolveExchange aplica los cambios de Pokémon y usos de objeto primero (nunca fallan, no hay
// daño de por medio) y recién después las jugadas de ataque que hayan quedado — regla real de
// Gen3: cambiar de Pokémon o usar un objeto siempre tiene prioridad sobre cualquier movimiento,
// sin importar Velocidad; ambos consumen el turno entero (no se ataca Y se cambia/cura a la vez).
func (s *Service) resolveExchange(sess *Session) *TurnResult {
	pending := [2]*actionInput{sess.sides[0].pending, sess.sides[1].pending}
	sess.sides[0].pending, sess.sides[1].pending = nil, nil

	var rawEvents []battle.Event
	events := make([]TurnEvent, 0, 4)
	actedPreMove := [2]bool{} // cambió de Pokémon o usó un objeto: no ataca este intercambio

	for i, p := range pending {
		switch p.kind {
		case actionSwitch:
			sess.sides[i].activeIdx = p.teamSlot
			sess.sides[i].needsSwitch = false
			actedPreMove[i] = true
			events = append(events, TurnEvent{
				Type: battle.EventSwitch, ActorCharID: sess.sides[i].characterID,
				TargetSpecies: sess.sides[i].species[p.teamSlot], TargetNickname: sess.sides[i].nicknames[p.teamSlot],
			})
		case actionItem:
			actedPreMove[i] = true
			events = append(events, s.applyItem(sess, i, p.itemID, p.teamSlot))
		}
	}

	activeFighters := [2]*battle.Fighter{
		sess.sides[0].fighters[sess.sides[0].activeIdx],
		sess.sides[1].fighters[sess.sides[1].activeIdx],
	}

	switch {
	case actedPreMove[0] && actedPreMove[1]:
		// Ninguno de los dos ataca este intercambio (cambio y/o objeto de ambos lados).
	case actedPreMove[0] && !actedPreMove[1]:
		rawEvents = append(rawEvents, battle.ResolveSingleAction(activeFighters, 1, battle.Action{MoveSlot: pending[1].moveSlot}, sess.rng)...)
	case !actedPreMove[0] && actedPreMove[1]:
		rawEvents = append(rawEvents, battle.ResolveSingleAction(activeFighters, 0, battle.Action{MoveSlot: pending[0].moveSlot}, sess.rng)...)
	default:
		rawEvents = append(rawEvents, battle.ResolveTurn(activeFighters, [2]battle.Action{{MoveSlot: pending[0].moveSlot}, {MoveSlot: pending[1].moveSlot}}, sess.rng)...)
	}

	for _, e := range rawEvents {
		events = append(events, TurnEvent{
			Type: e.Type, ActorCharID: sess.sides[e.FighterIdx].characterID,
			MoveID: e.MoveID, Damage: e.Damage, Effectiveness: e.Effectiveness, Fainted: e.Fainted, Amount: e.Amount,
		})
	}

	result := &TurnResult{
		Events: events,
		HPByCharacter: map[string]int{
			sess.sides[0].characterID: activeFighters[0].CurrentHP,
			sess.sides[1].characterID: activeFighters[1].CurrentHP,
		},
		Reason: "victory",
	}

	// Persistir el HP de TODO el equipo de ambos lados en cada intercambio (no solo al final):
	// si un jugador se desconecta a mitad de la pelea, ningún miembro del equipo debe quedar
	// con el HP de antes de empezar.
	//
	// Deliberado: esto es lo ÚNICO que una batalla PvP persiste. Nunca se toca `experience` ni
	// `level` acá (a diferencia del flujo PvE de "battle_result", ver PROTOCOL.md sección 10,
	// que si otorga experiencia/dinero validados) — dos jugadores podrían pelearse entre sí
	// cuantas veces quisieran sin ningún riesgo real (sin apostar Pokémon, sin perder nada más
	// que HP que se recupera solo) y usar eso para "levelear" de forma segura si el PvP diera
	// experiencia. Decisión explícita: solo el modo historia/PvE (que ya corre dentro del propio
	// emulador, ajeno a este paquete) hace subir de nivel.
	for _, side := range sess.sides {
		for i, pid := range side.pokemonIDs {
			_ = s.pokemon.UpdateHP(pid, side.fighters[i].CurrentHP)
		}
	}

	// ¿Algún lado se quedó sin Pokémon en pie? Si le queda equipo vivo, tiene que cambiar antes
	// de que el próximo intercambio se resuelva; si no le queda ninguno, la batalla terminó.
	for i := range 2 {
		if activeFighters[i].CurrentHP > 0 {
			continue
		}
		if anyAlive(sess.sides[i].fighters) {
			sess.sides[i].needsSwitch = true
			result.NeedsSwitch = sess.sides[i].characterID
		} else {
			sess.status = statusFinished
			result.Finished = true
			otherIdx := 1 - i
			result.WinnerCharID, result.LoserCharID = sess.sides[otherIdx].characterID, sess.sides[i].characterID
			delete(s.sessions, sess.ID)
			break
		}
	}

	return result
}

// applyItem aplica el efecto de un objeto ya validado y consumido (ver SubmitAction) sobre el
// Pokémon teamSlot del lado sideIdx, y devuelve el evento correspondiente para el log. La
// validación real (objetivo debilitado/con vida según corresponda, inventario disponible) ya
// se hizo al aceptar la acción — acá solo se aplica el número.
func (s *Service) applyItem(sess *Session, sideIdx, itemID, teamSlot int) TurnEvent {
	side := &sess.sides[sideIdx]
	fighter := side.fighters[teamSlot]
	info := inventory.Catalog[itemID]

	switch info.Effect {
	case inventory.EffectHealFlat:
		fighter.CurrentHP = min(fighter.MaxHP, fighter.CurrentHP+info.HealAmount)
	case inventory.EffectHealFull:
		fighter.CurrentHP = fighter.MaxHP
	case inventory.EffectRevive:
		fighter.CurrentHP = max(1, fighter.MaxHP/2)
	case inventory.EffectReviveFull:
		fighter.CurrentHP = fighter.MaxHP
	}

	return TurnEvent{
		Type: battle.EventItemUsed, ActorCharID: side.characterID, ItemID: itemID,
		TargetSpecies: side.species[teamSlot], TargetNickname: side.nicknames[teamSlot],
	}
}

func anyAlive(fighters []*battle.Fighter) bool {
	for _, f := range fighters {
		if f.CurrentHP > 0 {
			return true
		}
	}
	return false
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
