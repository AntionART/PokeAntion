// Package pokemon es la fuente autoritativa del equipo de cada personaje — el equivalente en
// servidor de RomLoader.Gen3Codec/StarterCatalog del cliente. El cliente nunca decide qué
// Pokémon tiene un jugador: lee este estado del servidor y lo inyecta en la RAM del emulador
// para mostrarlo (ver RomLoader.NewGameBootstrap), igual que ya pasa con dinero y sprite_color.
//
// Usa la tabla `pokemon` YA EXISTENTE (database/migrations/0001_init_schema.sql, diseñada
// mucho antes de esta sesión) — no una tabla propia. Esa tabla ya cubre equipo/PC/trade con
// ivs/evs/moves como JSONB; esta sesión solo le agregó personality/ot_id (migración 0007),
// que son los únicos campos que hacían falta y no existían: determinan la clave de cifrado y
// el orden de substructuras del formato binario real de Gen3 al armar el Pokémon en RAM.
package pokemon

import (
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	ErrInvalidSpecies  = errors.New("especie no es un inicial válido")
	ErrNoActivePokemon = errors.New("el personaje no tiene un pokémon activo (slot 0) todavía")
	ErrTeamFull        = errors.New("el equipo ya tiene 6 Pokémon (todavía no hay PC accesible para guardar más)")
	ErrInvalidMoveSlot = errors.New("slot de movimiento inválido")
	ErrNotOwner        = errors.New("el pokémon no te pertenece")
)

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// MoveSlot es la forma real que tiene cada movimiento dentro de la columna JSONB `moves` de la
// tabla pokemon — ver 0001_init_schema.sql.
type MoveSlot struct {
	MoveID    int `json:"move_id"`
	PPCurrent int `json:"pp_current"`
	PPMax     int `json:"pp_max"`
}

type Pokemon struct {
	ID          string     `json:"id"`
	Species     int        `json:"species"`
	Nickname    string     `json:"nickname"`
	Level       int        `json:"level"`
	Experience  int        `json:"experience"`
	Personality uint32     `json:"personality"`
	OtId        uint32     `json:"ot_id"`
	CurrentHP   int        `json:"current_hp"`
	MaxHP       int        `json:"max_hp"`
	Attack      int        `json:"attack"`
	Defense     int        `json:"defense"`
	Speed       int        `json:"speed"`
	SpAttack    int        `json:"sp_attack"`
	SpDefense   int        `json:"sp_defense"`
	Moves       []MoveSlot `json:"moves"`
	TeamSlot    int        `json:"team_slot"`
}

// MoveIDs/PPs: [4]int fijo (0 = sin movimiento) para interoperar con el motor de batalla
// (server/internal/battle) y con el formato de RAM que arma el cliente — ambos esperan un
// arreglo de tamaño fijo, no la lista de longitud variable que guarda la base de datos.
func (p Pokemon) MoveIDs() [4]int {
	var out [4]int
	for i := 0; i < len(p.Moves) && i < 4; i++ {
		out[i] = p.Moves[i].MoveID
	}
	return out
}

func (p Pokemon) PPs() [4]int {
	var out [4]int
	for i := 0; i < len(p.Moves) && i < 4; i++ {
		out[i] = p.Moves[i].PPCurrent
	}
	return out
}

// GetParty devuelve el equipo activo (location='team') ordenado por slot.
func (s *Service) GetParty(characterID string) ([]Pokemon, error) {
	rows, err := s.db.Query(
		`SELECT id, species_id, nickname, level, experience, personality, ot_id,
		        hp_current, hp_max, stat_attack, stat_defense, stat_speed, stat_sp_attack, stat_sp_defense,
		        moves, team_slot
		 FROM pokemon WHERE owner_char_id = $1 AND location = 'team' ORDER BY team_slot`,
		characterID,
	)
	if err != nil {
		return nil, fmt.Errorf("consultando equipo: %w", err)
	}
	defer rows.Close()

	var party []Pokemon
	for rows.Next() {
		var p Pokemon
		var personality, otId int64
		var nickname sql.NullString
		var movesRaw []byte
		if err := rows.Scan(
			&p.ID, &p.Species, &nickname, &p.Level, &p.Experience, &personality, &otId,
			&p.CurrentHP, &p.MaxHP, &p.Attack, &p.Defense, &p.Speed, &p.SpAttack, &p.SpDefense,
			&movesRaw, &p.TeamSlot,
		); err != nil {
			return nil, fmt.Errorf("leyendo fila de equipo: %w", err)
		}
		p.Nickname = nickname.String
		p.Personality, p.OtId = uint32(personality), uint32(otId)
		if err := json.Unmarshal(movesRaw, &p.Moves); err != nil {
			return nil, fmt.Errorf("parseando moves de %s: %w", p.ID, err)
		}
		party = append(party, p)
	}
	return party, rows.Err()
}

// GetActive devuelve el Pokémon en team_slot=0 (el único que pelea hoy — no hay switch de
// equipo todavía, ver battlesession.Service) o ErrNoActivePokemon si el personaje no tiene
// ninguno (todavía no eligió inicial).
func (s *Service) GetActive(characterID string) (Pokemon, error) {
	var p Pokemon
	var personality, otId int64
	var nickname sql.NullString
	var movesRaw []byte
	err := s.db.QueryRow(
		`SELECT id, species_id, nickname, level, experience, personality, ot_id,
		        hp_current, hp_max, stat_attack, stat_defense, stat_speed, stat_sp_attack, stat_sp_defense,
		        moves, team_slot
		 FROM pokemon WHERE owner_char_id = $1 AND location = 'team' AND team_slot = 0`,
		characterID,
	).Scan(
		&p.ID, &p.Species, &nickname, &p.Level, &p.Experience, &personality, &otId,
		&p.CurrentHP, &p.MaxHP, &p.Attack, &p.Defense, &p.Speed, &p.SpAttack, &p.SpDefense,
		&movesRaw, &p.TeamSlot,
	)
	if err == sql.ErrNoRows {
		return Pokemon{}, ErrNoActivePokemon
	}
	if err != nil {
		return Pokemon{}, fmt.Errorf("consultando pokémon activo: %w", err)
	}
	p.Nickname = nickname.String
	p.Personality, p.OtId = uint32(personality), uint32(otId)
	if err := json.Unmarshal(movesRaw, &p.Moves); err != nil {
		return Pokemon{}, fmt.Errorf("parseando moves de %s: %w", p.ID, err)
	}
	return p, nil
}

// UpdateHP persiste el HP restante al terminar (o abandonar) una batalla — es lo único que
// una batalla cambia de forma duradera hoy (sin daño por status, items ni experiencia todavía).
func (s *Service) UpdateHP(pokemonID string, currentHP int) error {
	_, err := s.db.Exec(`UPDATE pokemon SET hp_current = $1 WHERE id = $2`, currentHP, pokemonID)
	return err
}

// AddStarter crea el Pokémon inicial (nivel 5, slot 0 del equipo) — pensado para llamarse una
// sola vez, al crear personaje. personality/otId se generan al azar (determinan la clave de
// cifrado al armar el Pokémon en RAM, no necesitan "significar" nada, ver Gen3Codec). nature se
// deriva de personality%25, igual que el juego real (no se guarda al azar por separado).
func (s *Service) AddStarter(characterID string, species int) (Pokemon, error) {
	if !IsValidStarter(species) {
		return Pokemon{}, ErrInvalidSpecies
	}

	base, ok := SpeciesBaseStats(species)
	if !ok {
		return Pokemon{}, fmt.Errorf("especie %d no está en el catálogo de especies", species)
	}
	hp, attack, defense, speed, spAttack, spDefense := ComputeStatsAtLevel(base, 5)
	starterMoveList := starterMoves[species]
	personality := randomUint32()
	nature := int(personality % 25)

	moves := []MoveSlot{
		{MoveID: starterMoveList[0].MoveID, PPCurrent: starterMoveList[0].PP, PPMax: starterMoveList[0].PP},
		{MoveID: starterMoveList[1].MoveID, PPCurrent: starterMoveList[1].PP, PPMax: starterMoveList[1].PP},
	}
	movesJSON, err := json.Marshal(moves)
	if err != nil {
		return Pokemon{}, fmt.Errorf("serializando moves: %w", err)
	}

	p := Pokemon{
		ID: uuid.NewString(), Species: species, Nickname: SpeciesName(species), Level: 5, Experience: 0,
		Personality: personality, OtId: randomUint32(),
		CurrentHP: hp, MaxHP: hp, Attack: attack, Defense: defense, Speed: speed,
		SpAttack: spAttack, SpDefense: spDefense, Moves: moves, TeamSlot: 0,
	}

	_, err = s.db.Exec(
		`INSERT INTO pokemon (id, owner_char_id, species_id, nickname, level, experience,
		                       personality, ot_id, hp_current, hp_max,
		                       stat_attack, stat_defense, stat_speed, stat_sp_attack, stat_sp_defense,
		                       nature, moves, original_trainer_id, location, team_slot)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,'team',$19)`,
		p.ID, characterID, p.Species, p.Nickname, p.Level, p.Experience,
		int64(p.Personality), int64(p.OtId), p.CurrentHP, p.MaxHP,
		p.Attack, p.Defense, p.Speed, p.SpAttack, p.SpDefense,
		nature, movesJSON, characterID, p.TeamSlot,
	)
	if err != nil {
		return Pokemon{}, fmt.Errorf("guardando inicial: %w", err)
	}
	return p, nil
}

// AddCaught crea un Pokémon recién atrapado en un encuentro salvaje (ver
// server/internal/wildencounter) — a diferencia de AddStarter (nivel fijo 5, solo 3 especies),
// acepta cualquier especie/nivel real del catálogo, movimientos reales del learnset, e IVs
// reales al azar (0-31 por stat, no todos en 0 como los iniciales — sí importan acá porque
// viene de un encuentro real, no de un regalo fijo). Va a team si hay lugar (<6 miembros);
// si no, ErrTeamFull (no hay PC accesible todavía para guardar de más, ver roadmap).
func (s *Service) AddCaught(characterID string, species, level int, moves []MoveSlot, personality, otID uint32, ivs [6]int) (Pokemon, error) {
	base, ok := SpeciesBaseStats(species)
	if !ok {
		return Pokemon{}, fmt.Errorf("especie %d no está en el catálogo de especies", species)
	}

	var teamCount int
	if err := s.db.QueryRow(`SELECT count(*) FROM pokemon WHERE owner_char_id = $1 AND location = 'team'`, characterID).Scan(&teamCount); err != nil {
		return Pokemon{}, fmt.Errorf("contando equipo: %w", err)
	}
	if teamCount >= 6 {
		return Pokemon{}, ErrTeamFull
	}
	var nextSlot sql.NullInt64
	if err := s.db.QueryRow(`SELECT max(team_slot) FROM pokemon WHERE owner_char_id = $1 AND location = 'team'`, characterID).Scan(&nextSlot); err != nil {
		return Pokemon{}, fmt.Errorf("calculando próximo slot: %w", err)
	}
	teamSlot := 0
	if nextSlot.Valid {
		teamSlot = int(nextSlot.Int64) + 1
	}

	hp, attack, defense, speed, spAttack, spDefense := ComputeStatsWithIVs(base, level, ivs)
	nature := int(personality % 25)
	movesJSON, err := json.Marshal(moves)
	if err != nil {
		return Pokemon{}, fmt.Errorf("serializando moves: %w", err)
	}
	ivsJSON, err := json.Marshal(map[string]int{
		"hp": ivs[0], "attack": ivs[1], "defense": ivs[2], "speed": ivs[3], "sp_attack": ivs[4], "sp_defense": ivs[5],
	})
	if err != nil {
		return Pokemon{}, fmt.Errorf("serializando IVs: %w", err)
	}

	p := Pokemon{
		ID: uuid.NewString(), Species: species, Nickname: SpeciesName(species), Level: level, Experience: 0,
		Personality: personality, OtId: otID,
		CurrentHP: hp, MaxHP: hp, Attack: attack, Defense: defense, Speed: speed,
		SpAttack: spAttack, SpDefense: spDefense, Moves: moves, TeamSlot: teamSlot,
	}

	_, err = s.db.Exec(
		`INSERT INTO pokemon (id, owner_char_id, species_id, nickname, level, experience,
		                       personality, ot_id, hp_current, hp_max,
		                       stat_attack, stat_defense, stat_speed, stat_sp_attack, stat_sp_defense,
		                       nature, moves, ivs, original_trainer_id, location, team_slot)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,'team',$20)`,
		p.ID, characterID, p.Species, p.Nickname, p.Level, p.Experience,
		int64(p.Personality), int64(p.OtId), p.CurrentHP, p.MaxHP,
		p.Attack, p.Defense, p.Speed, p.SpAttack, p.SpDefense,
		nature, movesJSON, ivsJSON, characterID, p.TeamSlot,
	)
	if err != nil {
		return Pokemon{}, fmt.Errorf("guardando pokémon atrapado: %w", err)
	}

	// pokedex_entries: marcar visto+atrapado — no falla la captura si esto falla (el Pokémon
	// ya es dueño del jugador de todas formas, la Pokédex es solo un registro informativo).
	_, err = s.db.Exec(
		`INSERT INTO pokedex_entries (owner_char_id, species_id, seen, caught, first_seen_at, first_caught_at)
		 VALUES ($1, $2, true, true, now(), now())
		 ON CONFLICT (owner_char_id, species_id) DO UPDATE SET
		   caught = true, seen = true,
		   first_caught_at = COALESCE(pokedex_entries.first_caught_at, now())`,
		characterID, species,
	)
	if err != nil {
		return p, fmt.Errorf("guardando entrada de pokédex (pokémon ya se guardó bien): %w", err)
	}
	return p, nil
}

// AddExperience suma experiencia real a un Pokémon tras vencer a uno salvaje (ver
// server/internal/wildencounter) — el ÚNICO lugar donde algo gana experiencia hoy (PvP nunca
// la otorga, ver el comentario en battlesession.resolveExchange). Sube de nivel con la curva de
// experiencia REAL de la especie (growth_rate del catálogo, ver expForLevel) — ya no una única
// curva universal para todas. learnedMoveIDs son los movimientos que la especie aprende en el
// rango de niveles saltado (puede ser más de uno si una sola pelea alcanza para varios
// niveles) — el LLAMADOR decide qué hacer con ellos (ver wildencounter.translateAndPersist +
// LearnMove), esta función no toca la columna `moves` para no mezclar responsabilidades (mismo
// criterio que AddCaught, que recibe moves ya armados en vez de armarlos acá).
func (s *Service) AddExperience(pokemonID string, gainedExp int) (leveledUp bool, newLevel int, learnedMoveIDs []int, err error) {
	var species, level, experience, hpCurrent, hpMax int
	err = s.db.QueryRow(`SELECT species_id, level, experience, hp_current, hp_max FROM pokemon WHERE id = $1`, pokemonID).
		Scan(&species, &level, &experience, &hpCurrent, &hpMax)
	if err != nil {
		return false, 0, nil, fmt.Errorf("consultando pokémon para experiencia: %w", err)
	}
	if level >= 100 {
		return false, level, nil, nil
	}

	// Especie sin growth_rate conocido (no debería pasar con el catálogo real): cae a Medium
	// Fast (0), la curva más común, en vez de fallar la operación entera.
	growthRate, _ := SpeciesGrowthRate(species)

	newExperience := experience + gainedExp
	newLvl := level
	for newLvl < 100 && newExperience >= expForLevel(growthRate, newLvl+1) {
		newLvl++
	}
	if newLvl == level {
		_, err = s.db.Exec(`UPDATE pokemon SET experience = $1 WHERE id = $2`, newExperience, pokemonID)
		return false, level, nil, err
	}

	base, ok := SpeciesBaseStats(species)
	if !ok {
		_, err = s.db.Exec(`UPDATE pokemon SET experience = $1 WHERE id = $2`, newExperience, pokemonID)
		return false, level, nil, err
	}
	// IVs reales no se leen acá (ver AddCaught) para simplificar el recalculo — usar IV=0 al
	// subir de nivel subestima levemente el stat real de un Pokémon atrapado con IVs altos;
	// aceptable por ahora (mismo tipo de simplificación que el resto de esta sesión).
	newHP, attack, defense, speed, spAttack, spDefense := ComputeStatsAtLevel(base, newLvl)
	hpDelta := newHP - hpMax // el HP sube el mismo delta que el máximo, no se resetea a full (regla real de Gen3)

	_, err = s.db.Exec(
		`UPDATE pokemon SET experience = $1, level = $2, hp_max = $3, hp_current = $4,
		                     stat_attack = $5, stat_defense = $6, stat_speed = $7, stat_sp_attack = $8, stat_sp_defense = $9
		 WHERE id = $10`,
		newExperience, newLvl, newHP, hpCurrent+hpDelta, attack, defense, speed, spAttack, spDefense, pokemonID,
	)
	if err != nil {
		return true, newLvl, nil, err
	}
	return true, newLvl, NewMovesLearnedBetween(species, level, newLvl), nil
}

// LearnMove agrega un movimiento nuevo al Pokémon SOLO si todavía tiene lugar (menos de 4
// movimientos) — si ya tiene 4, no hace nada y el llamador debe ofrecer reemplazar uno (ver
// ReplaceMove y wildencounter.translateAndPersist, que arma el prompt correspondiente).
// ok=true si realmente se aprendió acá mismo.
func (s *Service) LearnMove(pokemonID string, moveID, ppMax int) (ok bool, err error) {
	var movesRaw []byte
	if err := s.db.QueryRow(`SELECT moves FROM pokemon WHERE id = $1`, pokemonID).Scan(&movesRaw); err != nil {
		return false, fmt.Errorf("consultando movimientos de %s: %w", pokemonID, err)
	}
	var moves []MoveSlot
	if len(movesRaw) > 0 {
		if err := json.Unmarshal(movesRaw, &moves); err != nil {
			return false, fmt.Errorf("parseando movimientos de %s: %w", pokemonID, err)
		}
	}
	if len(moves) >= 4 {
		return false, nil
	}
	moves = append(moves, MoveSlot{MoveID: moveID, PPCurrent: ppMax, PPMax: ppMax})
	movesJSON, err := json.Marshal(moves)
	if err != nil {
		return false, err
	}
	if _, err := s.db.Exec(`UPDATE pokemon SET moves = $1 WHERE id = $2`, movesJSON, pokemonID); err != nil {
		return false, err
	}
	return true, nil
}

// MovesOf devuelve los movimientos actuales de un Pokémon — usado por
// wildencounter.translateAndPersist para armar el prompt de "reemplazar movimiento" cuando
// LearnMove no pudo agregar uno nuevo por falta de lugar (el cliente necesita saber CUÁLES 4
// movimientos tiene para poder ofrecerlos como opciones a reemplazar).
func (s *Service) MovesOf(pokemonID string) ([]MoveSlot, error) {
	var movesRaw []byte
	if err := s.db.QueryRow(`SELECT moves FROM pokemon WHERE id = $1`, pokemonID).Scan(&movesRaw); err != nil {
		return nil, fmt.Errorf("consultando movimientos de %s: %w", pokemonID, err)
	}
	var moves []MoveSlot
	if len(movesRaw) > 0 {
		if err := json.Unmarshal(movesRaw, &moves); err != nil {
			return nil, fmt.Errorf("parseando movimientos de %s: %w", pokemonID, err)
		}
	}
	return moves, nil
}

// ReplaceMove sobreescribe el movimiento en `slot` (0-3) con uno nuevo — usado cuando el
// jugador elige reemplazar un movimiento existente tras el prompt de LearnMove sin lugar.
// characterID tiene que ser el dueño real de pokemonID (chequeo de ownership explícito acá,
// no en el router — este mensaje llega con un pokemon_id elegido por el cliente, sin eso
// cualquier jugador podría reescribir el moveset de un Pokémon ajeno adivinando su ID).
// ErrInvalidMoveSlot si el Pokémon no tiene ese slot (ej. tiene menos de 4 movimientos, lo cual
// no debería pasar en este flujo: si tenía lugar, LearnMove ya lo habría usado directamente).
func (s *Service) ReplaceMove(characterID, pokemonID string, slot, moveID, ppMax int) error {
	var ownerID string
	var movesRaw []byte
	if err := s.db.QueryRow(`SELECT owner_char_id, moves FROM pokemon WHERE id = $1`, pokemonID).Scan(&ownerID, &movesRaw); err != nil {
		return fmt.Errorf("consultando pokémon %s: %w", pokemonID, err)
	}
	if ownerID != characterID {
		return ErrNotOwner
	}
	var moves []MoveSlot
	if len(movesRaw) > 0 {
		if err := json.Unmarshal(movesRaw, &moves); err != nil {
			return fmt.Errorf("parseando movimientos de %s: %w", pokemonID, err)
		}
	}
	if slot < 0 || slot >= len(moves) {
		return ErrInvalidMoveSlot
	}
	moves[slot] = MoveSlot{MoveID: moveID, PPCurrent: ppMax, PPMax: ppMax}
	movesJSON, err := json.Marshal(moves)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE pokemon SET moves = $1 WHERE id = $2`, movesJSON, pokemonID)
	return err
}

// expForLevel: fórmulas reales de las 6 curvas de experiencia de Gen3 (ver
// src/data/pokemon/experience_tables.h en el checkout de pokeemerald — EXP_SLOW/EXP_FAST/
// EXP_MEDIUM_FAST/EXP_MEDIUM_SLOW/EXP_ERRATIC/EXP_FLUCTUATING), una por growthRate real de la
// especie (ver SpeciesGrowthRate) en vez de una única curva universal. growthRate desconocido
// (fuera de 0-5) cae a Medium Fast. División entera igual que el C original (trunca, no
// redondea) — Go trunca `/` sobre enteros exactamente igual que C, traducción directa segura.
func expForLevel(growthRate, level int) int {
	// Niveles 0 y 1 son un caso especial hardcodeado en las 6 tablas reales de pokeemerald
	// (0 y 1 exp respectivamente, ANTES de que empiecen a aplicar las fórmulas de cada curva
	// desde nivel 2) — ver src/data/pokemon/experience_tables.h. Sin este corte, la fórmula de
	// Medium Slow da un valor negativo para n=1 (6/5 - 15 + 100 - 140 < 0).
	if level <= 1 {
		return level
	}
	n := level
	cube := n * n * n
	switch growthRate {
	case 1: // GROWTH_ERRATIC
		switch {
		case n <= 50:
			return (100 - n) * cube / 50
		case n <= 68:
			return (150 - n) * cube / 100
		case n <= 98:
			return ((1911 - 10*n) / 3) * cube / 500
		default:
			return (160 - n) * cube / 100
		}
	case 2: // GROWTH_FLUCTUATING
		switch {
		case n <= 15:
			return ((n+1)/3 + 24) * cube / 50
		case n <= 36:
			return (n + 14) * cube / 50
		default:
			return (n/2 + 32) * cube / 50
		}
	case 3: // GROWTH_MEDIUM_SLOW
		return 6*cube/5 - 15*n*n + 100*n - 140
	case 4: // GROWTH_FAST
		return 4 * cube / 5
	case 5: // GROWTH_SLOW
		return 5 * cube / 4
	default: // GROWTH_MEDIUM_FAST (0, y cualquier valor no reconocido)
		return cube
	}
}

func randomUint32() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint32(b[:])
}
