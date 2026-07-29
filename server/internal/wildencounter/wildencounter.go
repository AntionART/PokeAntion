// Package wildencounter es el equivalente, para Pokémon salvajes, de lo que battlesession es
// para PvP: 100% servidor-autoritativo. El cliente NUNCA decide ni reporta qué Pokémon
// salvaje aparece, si la captura funciona, ni sus stats — solo avisa "acá pasó algo" (ver
// wild_encounter_triggered en el protocolo) y el servidor tira el encuentro, corre la fórmula
// de captura real de Gen3, genera IVs/movimientos, y crea la fila en `pokemon` si atrapa.
//
// A diferencia de battlesession (2 personajes reales, turnos que esperan a ambos lados), acá
// hay UN solo jugador real + un Pokémon efímero (sin fila en la base hasta que se atrapa) — el
// "rival" no espera nada, así que cada acción del jugador se resuelve de inmediato (el Pokémon
// salvaje "decide" con una IA mínima: un movimiento al azar entre los que le quedan PP).
package wildencounter

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"github.com/google/uuid"

	"pokemon-online/server/internal/battle"
	"pokemon-online/server/internal/inventory"
	"pokemon-online/server/internal/pokemon"
)

var (
	ErrNoEncounterHere = errors.New("no hay tabla de encuentros para este mapa")
	ErrSessionNotFound = errors.New("no hay ningún encuentro salvaje activo con esa sesión")
	ErrNotParticipant  = errors.New("esta batalla salvaje no es tuya")
	ErrInvalidItem     = errors.New("objeto no válido para esta acción")
	ErrTeamFull        = pokemon.ErrTeamFull
	ErrInvalidRodTier  = errors.New("caña de pescar inválida")
	ErrMissingRod      = errors.New("no tenés esa caña de pescar")
)

// EncounterKind distingue de qué superficie/acción viene un encuentro — cada una tiene su
// propia tabla de especies real (ver MapEncounters). "fishing" es un caso aparte: no comparte
// la fórmula de tasa de las otras 3 (ver TryFishingEncounter).
type EncounterKind string

const (
	EncounterLand      EncounterKind = "land"
	EncounterWater     EncounterKind = "water"
	EncounterRockSmash EncounterKind = "rock_smash"
	EncounterFishing   EncounterKind = "fishing"
)

type Session struct {
	ID              string
	characterID     string
	playerPokemonID string
	player          *battle.Fighter
	wildSpecies     int
	wildLevel       int
	wildIVs         [6]int
	wild            *battle.Fighter
	rng             *rand.Rand
}

// PlayerView/WildView son el resumen para mandar por protocolo (mismo espíritu que
// battlesession.PokemonView) — el lado salvaje no tiene PokemonID todavía (no existe fila en
// `pokemon` hasta que se atrapa).
type PlayerView struct {
	PokemonID string
	Species   int
	Nickname  string
	Level     int
	CurrentHP int
	MaxHP     int
}

type WildView struct {
	Species   int
	Level     int
	CurrentHP int
	MaxHP     int
}

type Event struct {
	Type          battle.EventType
	IsPlayer      bool // true si el evento lo generó/afectó al Pokémon del jugador, false si al salvaje
	MoveID        int
	Damage        int
	Effectiveness float64
	Fainted       bool
	Amount        int
}

type TurnResult struct {
	Events    []Event
	PlayerHP  int
	WildHP    int
	Finished  bool
	Reason    string // "wild_fainted" (con ExpGained/LeveledUp) | "player_fainted" | "fled" | "caught"
	ExpGained int
	LeveledUp bool
	NewLevel  int
	// LearnedMoves: movimientos que el Pokémon aprendió automáticamente al subir de nivel en
	// esta pelea (tenía lugar libre, ver pokemon.LearnMove).
	LearnedMoves []int
	// PendingMoveLearns: movimientos que el Pokémon podría aprender pero ya tiene 4 y hace
	// falta que el jugador elija cuál reemplazar (o declinar) — ver PendingMoveLearn.
	PendingMoveLearns []PendingMoveLearn
	CaughtPokemon     *pokemon.Pokemon
}

// PendingMoveLearn es un movimiento nuevo esperando la decisión del jugador de a cuál de los 4
// movimientos actuales reemplazar (o ninguno) — ver pokemon.ReplaceMove y el mensaje de
// protocolo wild_move_replace_prompt.
type PendingMoveLearn struct {
	PokemonID      string
	NewMoveID      int
	CurrentMoveIDs [4]int
}

type Service struct {
	mu       sync.Mutex
	sessions map[string]*Session

	pokemon   *pokemon.Service
	inventory *inventory.Service
}

func NewService(pokemonSvc *pokemon.Service, inventorySvc *inventory.Service) *Service {
	return &Service{sessions: make(map[string]*Session), pokemon: pokemonSvc, inventory: inventorySvc}
}

// TryEncounter tira el encuentro real de un mapa para land/water/rock_smash (fórmula de
// src/wild_encounter.c: WildEncounterCheck — land_mons/water_mons llaman exactamente la misma
// función, y RockSmashWildEncounter también, así que las 3 comparten esta lógica; simplificada
// sin modificadores de habilidad/repelente/flauta, que no existen todavía en este proyecto) y,
// si toca, elige especie/nivel por los pesos reales de slot. Fishing usa TryFishingEncounter en
// su lugar (no comparte esta fórmula de tasa, ver FishingSlot). ok=false si no había tabla para
// ese mapa/tipo, o si la tirada de probabilidad no dio.
func TryEncounter(mapID string, kind EncounterKind, rng *rand.Rand) (species, level int, ok bool) {
	table, exists := encountersByMap[mapID]
	if !exists {
		return 0, 0, false
	}
	switch kind {
	case EncounterWater:
		return rollRateGatedSlots(table.WaterEncounterRate, table.WaterSlots, rng)
	case EncounterRockSmash:
		return rollRateGatedSlots(table.RockSmashEncounterRate, table.RockSmashSlots, rng)
	default: // EncounterLand, y cualquier valor no reconocido cae acá como default seguro
		return rollRateGatedSlots(table.EncounterRate, table.Slots, rng)
	}
}

// rollRateGatedSlots: encounterRate*16 tope 2880, Random()%2880 < eso — fórmula real, ver
// WildEncounterCheck — compartida por land/water/rock_smash, cada una con su propia tasa/tabla.
func rollRateGatedSlots(rate int, slots []EncounterSlot, rng *rand.Rand) (species, level int, ok bool) {
	if len(slots) == 0 {
		return 0, 0, false
	}
	chance := rate * 16
	if chance > 2880 {
		chance = 2880
	}
	if rng.Intn(2880) >= chance {
		return 0, 0, false
	}
	return pickWeightedSlot(slots, rng)
}

// TryFishingEncounter elige especie/nivel entre los slots de UNA caña (rodTier) — sin chequeo
// de tasa propio (ver comentario de FishingSlot/gendata: el "¿pica o no?" real no vive en esta
// tabla). El llamador (StartEncounter) valida antes que characterID realmente tenga esa caña.
func TryFishingEncounter(mapID, rodTier string, rng *rand.Rand) (species, level int, ok bool) {
	table, exists := encountersByMap[mapID]
	if !exists {
		return 0, 0, false
	}
	var tierSlots []EncounterSlot
	for _, fs := range table.FishingSlots {
		if fs.RodTier == rodTier {
			tierSlots = append(tierSlots, EncounterSlot{SpeciesID: fs.SpeciesID, MinLevel: fs.MinLevel, MaxLevel: fs.MaxLevel, Weight: fs.Weight})
		}
	}
	return pickWeightedSlot(tierSlots, rng)
}

func pickWeightedSlot(slots []EncounterSlot, rng *rand.Rand) (species, level int, ok bool) {
	totalWeight := 0
	for _, slot := range slots {
		totalWeight += slot.Weight
	}
	if totalWeight <= 0 {
		return 0, 0, false
	}
	roll := rng.Intn(totalWeight)
	for _, slot := range slots {
		if roll < slot.Weight {
			lvlRange := slot.MaxLevel - slot.MinLevel + 1
			lvl := slot.MinLevel
			if lvlRange > 1 {
				lvl += rng.Intn(lvlRange)
			}
			return slot.SpeciesID, lvl, true
		}
		roll -= slot.Weight
	}
	return 0, 0, false // no debería llegar acá si los pesos suman totalWeight correctamente
}

// rodItemIDFor mapea el nombre de caña (tal como viene en el protocolo/FishingSlot.RodTier) al
// item_id real de include/constants/items.h — usado para chequear posesión antes de pescar.
func rodItemIDFor(rodTier string) (int, bool) {
	switch rodTier {
	case "old_rod":
		return inventory.ItemOldRod, true
	case "good_rod":
		return inventory.ItemGoodRod, true
	case "super_rod":
		return inventory.ItemSuperRod, true
	default:
		return 0, false
	}
}

// StartEncounter arma el Fighter del jugador (su Pokémon activo, vía pokemon.GetActive — ya
// existe, mismo que usa battlesession) y el del salvaje (IVs al azar, moveset real de su nivel)
// y crea la sesión. characterID debe tener un Pokémon activo con HP > 0. rodTier solo se usa
// (y hace falta) cuando kind == EncounterFishing — se valida que characterID realmente tenga
// esa caña ANTES de tirar el encuentro (sin eso, cualquiera podría pescar con una Super Rod que
// nunca tuvo con solo mandar el string correcto).
func (s *Service) StartEncounter(characterID, mapID string, kind EncounterKind, rodTier string, rng *rand.Rand) (sessionID string, playerView PlayerView, wildView WildView, err error) {
	var species, level int
	var ok bool
	if kind == EncounterFishing {
		rodItemID, validTier := rodItemIDFor(rodTier)
		if !validTier {
			return "", PlayerView{}, WildView{}, ErrInvalidRodTier
		}
		has, herr := s.inventory.HasItem(characterID, rodItemID)
		if herr != nil {
			return "", PlayerView{}, WildView{}, herr
		}
		if !has {
			return "", PlayerView{}, WildView{}, ErrMissingRod
		}
		species, level, ok = TryFishingEncounter(mapID, rodTier, rng)
	} else {
		species, level, ok = TryEncounter(mapID, kind, rng)
	}
	if !ok {
		return "", PlayerView{}, WildView{}, ErrNoEncounterHere
	}

	activeMon, err := s.pokemon.GetActive(characterID)
	if err != nil {
		return "", PlayerView{}, WildView{}, err
	}
	if activeMon.CurrentHP <= 0 {
		return "", PlayerView{}, WildView{}, errors.New("tu Pokémon activo está debilitado, no puede pelear")
	}
	type1, type2 := pokemon.SpeciesTypes(activeMon.Species)
	playerFighter := battle.NewFighterFromPokemon(activeMon, type1, type2)

	wildFighter, wildIVs := NewWildFighter(species, level, rng)

	sess := &Session{
		ID: uuid.NewString(), characterID: characterID, playerPokemonID: activeMon.ID,
		player: playerFighter, wildSpecies: species, wildLevel: level, wildIVs: wildIVs, wild: wildFighter, rng: rng,
	}

	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()

	return sess.ID, PlayerView{
			PokemonID: activeMon.ID, Species: activeMon.Species, Nickname: activeMon.Nickname,
			Level: activeMon.Level, CurrentHP: activeMon.CurrentHP, MaxHP: activeMon.MaxHP,
		}, WildView{
			Species: species, Level: level, CurrentHP: wildFighter.CurrentHP, MaxHP: wildFighter.MaxHP,
		}, nil
}

// NewWildFighter genera un Pokémon salvaje efímero (sin fila en la base hasta que se atrapa,
// ver AttemptCatch): IVs 0-31 al azar por stat (a diferencia de un inicial regalado, que usa
// IV=0), moveset real según su nivel (ver pokemon.MovesForLevel). Devuelve también las IVs usadas —
// quien llama las necesita para persistir el mismo Pokémon si se atrapa (ver createCaught),
// sin recalcular con IVs distintas por accidente.
func NewWildFighter(species, level int, rng *rand.Rand) (*battle.Fighter, [6]int) {
	base, ok := pokemon.SpeciesBaseStats(species)
	if !ok {
		base = pokemon.BaseStats{} // no debería pasar con un species_id real del catálogo
	}
	ivs := [6]int{rng.Intn(32), rng.Intn(32), rng.Intn(32), rng.Intn(32), rng.Intn(32), rng.Intn(32)}
	hp, attack, defense, speed, spAttack, spDefense := pokemon.ComputeStatsWithIVs(base, level, ivs)

	moveIDs := pokemon.MovesForLevel(species, level)
	var moves [4]int
	var pp [4]int
	for i := 0; i < len(moveIDs) && i < 4; i++ {
		moves[i] = moveIDs[i]
		if m, ok := battle.MoveByID(moveIDs[i]); ok {
			pp[i] = m.PP
		}
	}

	type1, type2 := pokemon.SpeciesTypes(species)
	return &battle.Fighter{
		Combatant: battle.Combatant{
			Level: level, Attack: attack, Defense: defense, SpAttack: spAttack, SpDefense: spDefense, Speed: speed,
			Type1: type1, Type2: type2,
		},
		CurrentHP: hp, MaxHP: hp, Moves: moves, PP: pp,
	}, ivs
}

func (s *Service) get(sessionID, characterID string) (*Session, error) {
	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if sess.characterID != characterID {
		return nil, ErrNotParticipant
	}
	return sess, nil
}

// SubmitMove resuelve un ataque del jugador contra el Pokémon salvaje, y la respuesta de este
// (IA mínima: un movimiento al azar entre los que le quedan PP) — a diferencia de
// battlesession, no hay que esperar a "el otro lado": el salvaje siempre tiene su jugada lista.
func (s *Service) SubmitMove(sessionID, characterID string, moveSlot int) (*TurnResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, err := s.get(sessionID, characterID)
	if err != nil {
		return nil, err
	}

	wildAction := battle.Action{MoveSlot: s.pickWildMove(sess)}
	fighters := [2]*battle.Fighter{sess.player, sess.wild}
	rawEvents := battle.ResolveTurn(fighters, [2]battle.Action{{MoveSlot: moveSlot}, wildAction}, sess.rng)

	result := s.translateAndPersist(sess, rawEvents)
	return result, nil
}

// pickWildMove: IA mínima real (Gen3 wild Pokémon no razonan, eligen al azar entre movimientos
// con PP) — cualquier slot con PP > 0; si a ninguno le queda PP, manda el slot 0 igual (el
// motor de batalla ya emite EventNoPP correctamente en ese caso).
func (s *Service) pickWildMove(sess *Session) int {
	var withPP []int
	for i, moveID := range sess.wild.Moves {
		if moveID != 0 && sess.wild.PP[i] > 0 {
			withPP = append(withPP, i)
		}
	}
	if len(withPP) == 0 {
		return 0
	}
	return withPP[sess.rng.Intn(len(withPP))]
}

// ThrowBall consume el objeto (falla limpio si no lo tiene) y tira la fórmula de captura real
// de Gen3 — si atrapa, termina la sesión y crea la fila en `pokemon`; si falla, el salvaje
// igual ataca (usar un objeto consume el turno entero, misma regla que en battlesession).
func (s *Service) ThrowBall(sessionID, characterID string, ballItemID int) (*TurnResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, err := s.get(sessionID, characterID)
	if err != nil {
		return nil, err
	}
	info, ok := inventory.Catalog[ballItemID]
	if !ok || info.Effect != inventory.EffectBall {
		return nil, ErrInvalidItem
	}
	if err := s.inventory.Consume(characterID, ballItemID); err != nil {
		return nil, err
	}

	caught := AttemptCatch(info.BallBonus, sess.wildSpecies, sess.wild.CurrentHP, sess.wild.MaxHP, sess.rng)
	if caught {
		newMon, err := s.createCaught(sess)
		delete(s.sessions, sessionID)
		if err != nil {
			return nil, err
		}
		return &TurnResult{Reason: "caught", CaughtPokemon: &newMon, Finished: true, PlayerHP: sess.player.CurrentHP, WildHP: sess.wild.CurrentHP}, nil
	}

	// Falló la Ball: el salvaje ataca igual (el jugador gastó su turno en el objeto).
	wildAction := battle.Action{MoveSlot: s.pickWildMove(sess)}
	rawEvents := battle.ResolveSingleAction([2]*battle.Fighter{sess.player, sess.wild}, 1, wildAction, sess.rng)
	result := s.translateAndPersist(sess, rawEvents)
	return result, nil
}

// createCaught arma el registro real: mismas IVs que ya se usaron en NewWildFighter para
// calcular los stats de sess.wild (guardadas en sess.wildIVs), así pokemon.AddCaught recalcula
// EXACTAMENTE los mismos stats que el Fighter ya tenía — una sola fuente de verdad
// (base+nivel+IVs), no dos cálculos independientes que podrían desincronizarse.
func (s *Service) createCaught(sess *Session) (pokemon.Pokemon, error) {
	moves := make([]pokemon.MoveSlot, 0, 4)
	for _, moveID := range sess.wild.Moves {
		if moveID == 0 {
			continue
		}
		pp := 0
		if m, ok := battle.MoveByID(moveID); ok {
			pp = m.PP
		}
		moves = append(moves, pokemon.MoveSlot{MoveID: moveID, PPCurrent: pp, PPMax: pp})
	}
	personality := randomUint32()
	otID := randomUint32()
	return s.pokemon.AddCaught(sess.characterID, sess.wildSpecies, sess.wildLevel, moves, personality, otID, sess.wildIVs)
}

// Flee abandona el encuentro sin premio ni castigo (a diferencia de battlesession.Flee, acá no
// hay un rival humano al que declarar ganador).
func (s *Service) Flee(sessionID, characterID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.get(sessionID, characterID); err != nil {
		return err
	}
	delete(s.sessions, sessionID)
	return nil
}

// CancelActiveForCharacter borra cualquier encuentro salvaje activo de characterID — se usa al
// desconectarse (mismo patrón que battlesession/trade), no hay nadie más a quien avisar (a
// diferencia de esos dos, acá el "rival" no es un jugador real).
func (s *Service) CancelActiveForCharacter(characterID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		if sess.characterID == characterID {
			delete(s.sessions, id)
		}
	}
}

// translateAndPersist traduce los battle.Event a wildencounter.Event, persiste el HP del
// jugador (UpdateHP, igual que battlesession) y decide si la batalla terminó (alguien se
// debilitó) — otorgando experiencia real si el salvaje cayó (ver pokemon.AddExperience).
func (s *Service) translateAndPersist(sess *Session, rawEvents []battle.Event) *TurnResult {
	events := make([]Event, 0, len(rawEvents))
	for _, e := range rawEvents {
		events = append(events, Event{
			Type: e.Type, IsPlayer: e.FighterIdx == 0,
			MoveID: e.MoveID, Damage: e.Damage, Effectiveness: e.Effectiveness, Fainted: e.Fainted, Amount: e.Amount,
		})
	}

	_ = s.pokemon.UpdateHP(sess.playerPokemonID, sess.player.CurrentHP)

	result := &TurnResult{Events: events, PlayerHP: sess.player.CurrentHP, WildHP: sess.wild.CurrentHP}

	if sess.wild.CurrentHP <= 0 {
		result.Finished = true
		result.Reason = "wild_fainted"
		if info, ok := pokemon.SpeciesExpYield(sess.wildSpecies); ok {
			gained := info * sess.wildLevel / 7
			leveledUp, newLevel, learnedMoveIDs, err := s.pokemon.AddExperience(sess.playerPokemonID, gained)
			if err == nil {
				result.ExpGained, result.LeveledUp, result.NewLevel = gained, leveledUp, newLevel
				for _, moveID := range learnedMoveIDs {
					pp := 0
					if m, ok := battle.MoveByID(moveID); ok {
						pp = m.PP
					}
					learned, _ := s.pokemon.LearnMove(sess.playerPokemonID, moveID, pp)
					if learned {
						result.LearnedMoves = append(result.LearnedMoves, moveID)
						continue
					}
					// No había lugar (ya tenía 4): armar el prompt para que el jugador elija a
					// cuál reemplazar, en vez de descartar el movimiento nuevo en silencio.
					if current, err := s.pokemon.MovesOf(sess.playerPokemonID); err == nil && len(current) == 4 {
						var currentIDs [4]int
						for i, m := range current {
							currentIDs[i] = m.MoveID
						}
						result.PendingMoveLearns = append(result.PendingMoveLearns, PendingMoveLearn{
							PokemonID: sess.playerPokemonID, NewMoveID: moveID, CurrentMoveIDs: currentIDs,
						})
					}
				}
			}
		}
		delete(s.sessions, sess.ID)
	} else if sess.player.CurrentHP <= 0 {
		result.Finished = true
		result.Reason = "player_fainted"
		delete(s.sessions, sess.ID)
	}

	return result
}

// AttemptCatch: fórmula real de captura de Gen3 (ver src/battle_script_commands.c
// CriticalCapture/item_use.c) simplificada — sin bonus de estado (no hay estados
// implementados todavía). ballBonus: 1=Poké Ball, 1.5=Great, 2=Ultra, 255=Master (siempre
// atrapa, se corta antes de la fórmula).
func AttemptCatch(ballBonus float64, wildSpecies, wildCurrentHP, wildMaxHP int, rng *rand.Rand) bool {
	if ballBonus >= 255 {
		return true // Master Ball
	}
	catchRate, ok := pokemon.SpeciesCatchRate(wildSpecies)
	if !ok || wildMaxHP <= 0 {
		catchRate = 45 // no debería pasar con un species_id real; valor neutro de respaldo
	}

	a := float64((3*wildMaxHP-2*wildCurrentHP)*catchRate) * ballBonus / float64(3*wildMaxHP)
	if a >= 255 {
		return true // el multiplicador ya garantiza la captura antes de las tiradas de "shake"
	}
	b := 1048560.0 / math.Sqrt(math.Sqrt(16711680.0/a))
	for i := 0; i < 4; i++ {
		if float64(rng.Intn(65536)) >= b {
			return false // la ball se abrió en esta sacudida
		}
	}
	return true
}

func randomUint32() uint32 {
	return uint32(rand.Int63())
}
