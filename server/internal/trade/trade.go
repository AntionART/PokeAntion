package trade

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidState    = errors.New("la sesión de trade no está en el estado esperado")
	ErrNotOwner        = errors.New("el pokémon no pertenece a este jugador")
	ErrAlreadyLocked   = errors.New("el pokémon ya está bloqueado en otra transacción")
	ErrSessionNotFound = errors.New("sesión de trade no encontrada")
)

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// RequestTrade crea una nueva sesión de trade en estado "pending".
func (s *Service) RequestTrade(charAID, charBID string) (string, error) {
	sessionID := uuid.NewString()
	_, err := s.db.Exec(
		`INSERT INTO trade_sessions (id, char_a_id, char_b_id, status) VALUES ($1, $2, $3, 'pending')`,
		sessionID, charAID, charBID,
	)
	if err != nil {
		return "", fmt.Errorf("creando sesión de trade: %w", err)
	}
	s.log(sessionID, "created", nil)
	return sessionID, nil
}

func (s *Service) AcceptTrade(sessionID string) error {
	res, err := s.db.Exec(
		`UPDATE trade_sessions SET status = 'accepted' WHERE id = $1 AND status = 'pending'`,
		sessionID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrInvalidState
	}
	s.log(sessionID, "accepted", nil)
	return nil
}

// SetOffer bloquea el Pokémon ofrecido (location='in_trade') dentro de una transacción,
// para que no pueda usarse simultáneamente en otro trade, PC o batalla.
func (s *Service) SetOffer(sessionID, characterID, pokemonID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Verificar dueño y que no esté ya bloqueado
	var owner, location string
	err = tx.QueryRow(`SELECT owner_char_id, location FROM pokemon WHERE id = $1 FOR UPDATE`, pokemonID).
		Scan(&owner, &location)
	if err != nil {
		return fmt.Errorf("consultando pokémon: %w", err)
	}
	if owner != characterID {
		return ErrNotOwner
	}
	if location == "in_trade" {
		return ErrAlreadyLocked
	}

	// Determinar si el jugador es "A" o "B" de la sesión para actualizar la columna correcta
	var charA, charB string
	err = tx.QueryRow(`SELECT char_a_id, char_b_id FROM trade_sessions WHERE id = $1 AND status IN ('accepted','offering') FOR UPDATE`, sessionID).
		Scan(&charA, &charB)
	if err == sql.ErrNoRows {
		return ErrSessionNotFound
	}
	if err != nil {
		return err
	}

	if characterID == charA {
		_, err = tx.Exec(`UPDATE trade_sessions SET status = 'offering', offer_a_pokemon_id = $1, confirmed_a = false, confirmed_b = false WHERE id = $2`, pokemonID, sessionID)
	} else if characterID == charB {
		_, err = tx.Exec(`UPDATE trade_sessions SET status = 'offering', offer_b_pokemon_id = $1, confirmed_a = false, confirmed_b = false WHERE id = $2`, pokemonID, sessionID)
	} else {
		return errors.New("el jugador no pertenece a esta sesión de trade")
	}
	if err != nil {
		return err
	}

	_, err = tx.Exec(`UPDATE pokemon SET location = 'in_trade', locked_for_trade_id = $1 WHERE id = $2`, sessionID, pokemonID)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	s.log(sessionID, "offer_set", nil)
	return nil
}

// OwnedPokemon es el resumen de un Pokémon para mostrar en UI (selector de oferta de trade,
// listas, etc.) — no el detalle completo de combate (IVs/EVs/movimientos), que no hace falta
// para elegir qué ofrecer.
type OwnedPokemon struct {
	ID        string
	SpeciesID int
	Nickname  string
	Level     int
	Location  string
}

// ListOwned devuelve los Pokémon de characterID disponibles para ofrecer en un trade (se
// excluyen los ya bloqueados en otra sesión — location='in_trade' — porque no se pueden
// ofrecer dos veces a la vez).
func (s *Service) ListOwned(characterID string) ([]OwnedPokemon, error) {
	rows, err := s.db.Query(
		`SELECT id, species_id, COALESCE(nickname, ''), level, location FROM pokemon
		 WHERE owner_char_id = $1 AND location != 'in_trade'
		 ORDER BY team_slot NULLS LAST, caught_at`,
		characterID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OwnedPokemon
	for rows.Next() {
		var p OwnedPokemon
		if err := rows.Scan(&p.ID, &p.SpeciesID, &p.Nickname, &p.Level, &p.Location); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetSummary devuelve el resumen de un único Pokémon por ID, usado para avisarle al otro
// participante de un trade qué le acaban de ofrecer (ver TradeOfferUpdatedPayload).
func (s *Service) GetSummary(pokemonID string) (OwnedPokemon, error) {
	var p OwnedPokemon
	err := s.db.QueryRow(
		`SELECT id, species_id, COALESCE(nickname, ''), level, location FROM pokemon WHERE id = $1`,
		pokemonID,
	).Scan(&p.ID, &p.SpeciesID, &p.Nickname, &p.Level, &p.Location)
	return p, err
}

// Participants devuelve los dos personajes de una sesión de trade, para poder notificarlos
// a ambos (ver trade_accepted/trade_offer_updated en router.go).
func (s *Service) Participants(sessionID string) (charA, charB string, err error) {
	err = s.db.QueryRow(`SELECT char_a_id, char_b_id FROM trade_sessions WHERE id = $1`, sessionID).Scan(&charA, &charB)
	return charA, charB, err
}

// TradeResult contiene los datos necesarios para notificar a ambos jugadores
// cuando una sesión de trade se completa.
type TradeResult struct {
	CharAID, CharBID       string
	CharAReceivedPokemonID string // el pokémon que A recibió (antes era de B)
	CharBReceivedPokemonID string // el pokémon que B recibió (antes era de A)
}

// Confirm marca la confirmación de un jugador. Cuando ambos confirmaron,
// ejecuta el intercambio real como una única transacción atómica (todo o nada).
func (s *Service) Confirm(sessionID, characterID string) (result *TradeResult, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var charA, charB, offerA, offerB string
	var confirmedA, confirmedB bool
	err = tx.QueryRow(
		`SELECT char_a_id, char_b_id, offer_a_pokemon_id, offer_b_pokemon_id, confirmed_a, confirmed_b
		 FROM trade_sessions WHERE id = $1 AND status = 'offering' FOR UPDATE`,
		sessionID,
	).Scan(&charA, &charB, &offerA, &offerB, &confirmedA, &confirmedB)
	if err == sql.ErrNoRows {
		return nil, ErrInvalidState
	}
	if err != nil {
		return nil, err
	}
	if offerA == "" || offerB == "" {
		return nil, errors.New("ambos jugadores deben ofrecer un pokémon antes de confirmar")
	}

	if characterID == charA {
		confirmedA = true
	} else if characterID == charB {
		confirmedB = true
	} else {
		return nil, errors.New("el jugador no pertenece a esta sesión de trade")
	}

	_, err = tx.Exec(`UPDATE trade_sessions SET confirmed_a = $1, confirmed_b = $2 WHERE id = $3`, confirmedA, confirmedB, sessionID)
	if err != nil {
		return nil, err
	}

	if !(confirmedA && confirmedB) {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		s.log(sessionID, "confirmed", nil)
		return nil, nil
	}

	// --- Ambos confirmaron: ejecutar el intercambio real, atómico ---
	// Se cambia el dueño de cada Pokémon y se libera el bloqueo, todo en la misma transacción.
	// Si cualquier paso falla, el defer tx.Rollback() deshace TODO: no puede quedar
	// un Pokémon "a medio camino" ni duplicado.
	_, err = tx.Exec(`UPDATE pokemon SET owner_char_id = $1, location = 'pc', pc_box_id = NULL, pc_box_slot = NULL, locked_for_trade_id = NULL WHERE id = $2`, charB, offerA)
	if err != nil {
		return nil, fmt.Errorf("transfiriendo pokémon A->B: %w", err)
	}
	_, err = tx.Exec(`UPDATE pokemon SET owner_char_id = $1, location = 'pc', pc_box_id = NULL, pc_box_slot = NULL, locked_for_trade_id = NULL WHERE id = $2`, charA, offerB)
	if err != nil {
		return nil, fmt.Errorf("transfiriendo pokémon B->A: %w", err)
	}
	_, err = tx.Exec(`UPDATE trade_sessions SET status = 'completed', completed_at = now() WHERE id = $1`, sessionID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.log(sessionID, "completed", nil)
	return &TradeResult{
		CharAID: charA, CharBID: charB,
		CharAReceivedPokemonID: offerB, CharBReceivedPokemonID: offerA,
	}, nil
}

// Cancel aborta la sesión y libera cualquier Pokémon bloqueado. Es el mecanismo
// de rollback explícito ante desconexión, timeout o cancelación manual (decline).
// Devuelve los dos participantes para que el llamador pueda notificarlos.
func (s *Service) Cancel(sessionID, reason string) (charA, charB string, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()

	err = tx.QueryRow(
		`SELECT char_a_id, char_b_id FROM trade_sessions
		 WHERE id = $1 AND status NOT IN ('completed','cancelled','failed_rollback') FOR UPDATE`,
		sessionID,
	).Scan(&charA, &charB)
	if err == sql.ErrNoRows {
		return "", "", ErrSessionNotFound
	}
	if err != nil {
		return "", "", err
	}

	_, err = tx.Exec(`UPDATE pokemon SET location = 'pc', locked_for_trade_id = NULL WHERE locked_for_trade_id = $1`, sessionID)
	if err != nil {
		return "", "", err
	}
	_, err = tx.Exec(`UPDATE trade_sessions SET status = 'cancelled', cancelled_reason = $1 WHERE id = $2`, reason, sessionID)
	if err != nil {
		return "", "", err
	}

	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	s.log(sessionID, "cancelled", nil)
	return charA, charB, nil
}

// CancelledSession identifica, tras una cancelación en lote, la sesión afectada
// y el otro participante (no el que disparó la cancelación) para notificarlo.
type CancelledSession struct {
	SessionID   string
	OtherCharID string
}

// CancelActiveForCharacter cancela todas las sesiones de trade no terminales en las
// que participa characterID. Se usa al desconectarse un jugador a mitad de un trade,
// para no dejar el Pokémon del otro jugador bloqueado indefinidamente.
func (s *Service) CancelActiveForCharacter(characterID, reason string) ([]CancelledSession, error) {
	rows, err := s.db.Query(
		`SELECT id, char_a_id, char_b_id FROM trade_sessions
		 WHERE (char_a_id = $1 OR char_b_id = $1) AND status NOT IN ('completed','cancelled','failed_rollback')`,
		characterID,
	)
	if err != nil {
		return nil, err
	}
	type sessionRow struct{ id, charA, charB string }
	var pending []sessionRow
	for rows.Next() {
		var sr sessionRow
		if err := rows.Scan(&sr.id, &sr.charA, &sr.charB); err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, sr)
	}
	rows.Close()

	var out []CancelledSession
	for _, sr := range pending {
		charA, charB, err := s.Cancel(sr.id, reason)
		if err != nil {
			continue // pudo haber sido cancelada/completada por otra vía justo antes
		}
		other := charB
		if characterID == charB {
			other = charA
		}
		out = append(out, CancelledSession{SessionID: sr.id, OtherCharID: other})
	}
	return out, nil
}

// SweepExpired cancela toda sesión de trade que lleve más de maxAge sin completarse.
// Pensado para correr periódicamente (ver main.go), evita que un trade abandonado
// (ninguna de las dos partes se desconecta, pero tampoco confirma) bloquee un Pokémon
// para siempre.
func (s *Service) SweepExpired(maxAge time.Duration) ([]CancelledSession, error) {
	seconds := int(maxAge.Seconds())
	rows, err := s.db.Query(
		`SELECT id, char_a_id, char_b_id FROM trade_sessions
		 WHERE status NOT IN ('completed','cancelled','failed_rollback')
		   AND created_at < now() - make_interval(secs => $1)`,
		seconds,
	)
	if err != nil {
		return nil, err
	}
	type sessionRow struct{ id, charA, charB string }
	var pending []sessionRow
	for rows.Next() {
		var sr sessionRow
		if err := rows.Scan(&sr.id, &sr.charA, &sr.charB); err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, sr)
	}
	rows.Close()

	var out []CancelledSession
	for _, sr := range pending {
		charA, charB, err := s.Cancel(sr.id, "timeout")
		if err != nil {
			continue
		}
		out = append(out, CancelledSession{SessionID: sr.id, OtherCharID: charB}, CancelledSession{SessionID: sr.id, OtherCharID: charA})
	}
	return out, nil
}

func (s *Service) log(sessionID, event string, detail any) {
	_, _ = s.db.Exec(
		`INSERT INTO trade_log (id, trade_session_id, event) VALUES ($1, $2, $3)`,
		uuid.NewString(), sessionID, event,
	)
}
