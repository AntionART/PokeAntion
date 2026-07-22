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

	hp, attack, defense, speed, spAttack, spDefense := computeStatsAtLevel(starterBaseStats[species], 5)
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
		ID: uuid.NewString(), Species: species, Nickname: starterName[species], Level: 5, Experience: 0,
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

func randomUint32() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.LittleEndian.Uint32(b[:])
}
