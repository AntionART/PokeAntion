package social

import (
	"database/sql"
	"errors"
	"sync"

	"github.com/google/uuid"
)

var (
	ErrAlreadyInParty = errors.New("ya estás en un grupo")
	ErrInviteNotFound = errors.New("no hay invitación pendiente de ese grupo")
	ErrNotPartyLeader = errors.New("solo el líder del grupo puede invitar")
)

// PartyService administra grupos (party_groups/party_members). Las invitaciones
// pendientes NO tienen tabla propia en el esquema (a diferencia de trade_sessions),
// así que se mantienen en memoria: son efímeras por naturaleza y se pierden con el
// proceso, igual que ya pasa con la presencia del Hub.
type PartyService struct {
	db *sql.DB

	mu      sync.Mutex
	invites map[string]string // targetCharacterID -> partyID pendiente de aceptar/declinar
}

func NewPartyService(db *sql.DB) *PartyService {
	return &PartyService{db: db, invites: make(map[string]string)}
}

func (s *PartyService) partyOfCharacter(characterID string) (string, error) {
	var partyID string
	err := s.db.QueryRow(`SELECT party_id FROM party_members WHERE char_id = $1`, characterID).Scan(&partyID)
	return partyID, err
}

// Invite crea un grupo nuevo liderado por fromCharacterID si todavía no lidera/pertenece
// a ninguno, o reutiliza el existente si fromCharacterID ya es su líder. Registra una
// invitación pendiente en memoria para targetCharacterID.
func (s *PartyService) Invite(fromCharacterID, targetCharacterID string) (partyID string, err error) {
	partyID, err = s.partyOfCharacter(fromCharacterID)
	switch {
	case err == sql.ErrNoRows:
		partyID = uuid.NewString()
		tx, txErr := s.db.Begin()
		if txErr != nil {
			return "", txErr
		}
		defer tx.Rollback()
		if _, err = tx.Exec(`INSERT INTO party_groups (id, leader_char_id) VALUES ($1, $2)`, partyID, fromCharacterID); err != nil {
			return "", err
		}
		if _, err = tx.Exec(`INSERT INTO party_members (party_id, char_id) VALUES ($1, $2)`, partyID, fromCharacterID); err != nil {
			return "", err
		}
		if err = tx.Commit(); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	default:
		var leaderID string
		if err = s.db.QueryRow(`SELECT leader_char_id FROM party_groups WHERE id = $1`, partyID).Scan(&leaderID); err != nil {
			return "", err
		}
		if leaderID != fromCharacterID {
			return "", ErrNotPartyLeader
		}
	}

	if _, err := s.partyOfCharacter(targetCharacterID); err == nil {
		return "", ErrAlreadyInParty
	}

	s.mu.Lock()
	s.invites[targetCharacterID] = partyID
	s.mu.Unlock()

	return partyID, nil
}

// Accept confirma una invitación pendiente y agrega al jugador como miembro del grupo.
func (s *PartyService) Accept(characterID, partyID string) error {
	s.mu.Lock()
	pending, ok := s.invites[characterID]
	if ok {
		delete(s.invites, characterID)
	}
	s.mu.Unlock()
	if !ok || pending != partyID {
		return ErrInviteNotFound
	}

	if _, err := s.partyOfCharacter(characterID); err == nil {
		return ErrAlreadyInParty
	}

	_, err := s.db.Exec(`INSERT INTO party_members (party_id, char_id) VALUES ($1, $2)`, partyID, characterID)
	return err
}

// Decline descarta una invitación pendiente sin unirse al grupo.
func (s *PartyService) Decline(characterID, partyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.invites[characterID]
	if !ok || pending != partyID {
		return ErrInviteNotFound
	}
	delete(s.invites, characterID)
	return nil
}

// Leave saca a characterID de su grupo. Si era el líder y quedan más miembros,
// transfiere el liderazgo al miembro más antiguo restante; si era el último
// miembro, disuelve el grupo. partyID viene vacío si el jugador no estaba en ninguno.
func (s *PartyService) Leave(characterID string) (partyID string, disbanded bool, err error) {
	partyID, err = s.partyOfCharacter(characterID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()

	if _, err = tx.Exec(`DELETE FROM party_members WHERE party_id = $1 AND char_id = $2`, partyID, characterID); err != nil {
		return "", false, err
	}

	var remaining int
	if err = tx.QueryRow(`SELECT count(*) FROM party_members WHERE party_id = $1`, partyID).Scan(&remaining); err != nil {
		return "", false, err
	}

	if remaining == 0 {
		if _, err = tx.Exec(`DELETE FROM party_groups WHERE id = $1`, partyID); err != nil {
			return "", false, err
		}
		disbanded = true
	} else {
		var leaderID string
		if err = tx.QueryRow(`SELECT leader_char_id FROM party_groups WHERE id = $1`, partyID).Scan(&leaderID); err != nil {
			return "", false, err
		}
		if leaderID == characterID {
			var newLeader string
			if err = tx.QueryRow(`SELECT char_id FROM party_members WHERE party_id = $1 ORDER BY joined_at ASC LIMIT 1`, partyID).Scan(&newLeader); err != nil {
				return "", false, err
			}
			if _, err = tx.Exec(`UPDATE party_groups SET leader_char_id = $1 WHERE id = $2`, newLeader, partyID); err != nil {
				return "", false, err
			}
		}
	}

	if err = tx.Commit(); err != nil {
		return "", false, err
	}
	return partyID, disbanded, nil
}

type PartyMember struct {
	CharacterID string
	IsLeader    bool
}

// Members devuelve los miembros actuales de un grupo, marcando quién es el líder.
func (s *PartyService) Members(partyID string) ([]PartyMember, error) {
	var leaderID string
	if err := s.db.QueryRow(`SELECT leader_char_id FROM party_groups WHERE id = $1`, partyID).Scan(&leaderID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT char_id FROM party_members WHERE party_id = $1`, partyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PartyMember
	for rows.Next() {
		var charID string
		if err := rows.Scan(&charID); err != nil {
			return nil, err
		}
		out = append(out, PartyMember{CharacterID: charID, IsLeader: charID == leaderID})
	}
	return out, rows.Err()
}
