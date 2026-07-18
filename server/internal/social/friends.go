// Package social implementa amigos y grupos (party), siguiendo el mismo patrón
// que internal/trade: un Service con métodos que operan directamente sobre las
// tablas del esquema (friendships, party_groups, party_members) en transacciones
// cuando hace falta atomicidad.
package social

import (
	"database/sql"
	"errors"
)

var (
	ErrUserNotFound     = errors.New("usuario no encontrado")
	ErrSelfFriend       = errors.New("no podés agregarte a vos mismo como amigo")
	ErrAlreadyRequested = errors.New("ya existe una relación de amistad (pendiente, aceptada o bloqueada) con ese usuario")
	ErrRequestNotFound  = errors.New("no hay una solicitud de amistad pendiente de ese usuario")
)

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func (s *Service) accountIDForCharacter(characterID string) (string, error) {
	var accountID string
	err := s.db.QueryRow(`SELECT account_id FROM characters WHERE id = $1`, characterID).Scan(&accountID)
	return accountID, err
}

// AccountIDForCharacter expone la resolución characterID -> account_id para que
// el router pueda armar notificaciones (ej. friend_status_update) sin duplicar la query.
func (s *Service) AccountIDForCharacter(characterID string) (string, error) {
	return s.accountIDForCharacter(characterID)
}

// FriendRequestResult identifica a ambas partes de una solicitud recién creada,
// incluyendo un personaje representativo del destinatario para poder notificarlo
// si está conectado ahora mismo.
type FriendRequestResult struct {
	FromAccountID string
	FromUsername  string
	ToAccountID   string
	ToCharacterID string
}

// Request crea una solicitud de amistad pendiente de fromCharacterID hacia la
// cuenta dueña de targetUsername.
func (s *Service) Request(fromCharacterID, targetUsername string) (FriendRequestResult, error) {
	fromAccountID, err := s.accountIDForCharacter(fromCharacterID)
	if err != nil {
		return FriendRequestResult{}, err
	}
	var fromUsername string
	if err := s.db.QueryRow(`SELECT username FROM accounts WHERE id = $1`, fromAccountID).Scan(&fromUsername); err != nil {
		return FriendRequestResult{}, err
	}

	var toAccountID string
	err = s.db.QueryRow(`SELECT id FROM accounts WHERE username = $1`, targetUsername).Scan(&toAccountID)
	if err == sql.ErrNoRows {
		return FriendRequestResult{}, ErrUserNotFound
	}
	if err != nil {
		return FriendRequestResult{}, err
	}
	if toAccountID == fromAccountID {
		return FriendRequestResult{}, ErrSelfFriend
	}

	var existing string
	err = s.db.QueryRow(`SELECT status FROM friendships WHERE account_id = $1 AND friend_id = $2`, fromAccountID, toAccountID).Scan(&existing)
	if err == nil {
		return FriendRequestResult{}, ErrAlreadyRequested
	}
	if err != nil && err != sql.ErrNoRows {
		return FriendRequestResult{}, err
	}

	if _, err := s.db.Exec(
		`INSERT INTO friendships (account_id, friend_id, status) VALUES ($1, $2, 'pending')`,
		fromAccountID, toAccountID,
	); err != nil {
		return FriendRequestResult{}, err
	}

	var toCharacterID sql.NullString
	_ = s.db.QueryRow(`SELECT id FROM characters WHERE account_id = $1 ORDER BY created_at ASC LIMIT 1`, toAccountID).Scan(&toCharacterID)

	return FriendRequestResult{
		FromAccountID: fromAccountID, FromUsername: fromUsername,
		ToAccountID: toAccountID, ToCharacterID: toCharacterID.String,
	}, nil
}

// Accept convierte en aceptada, de forma bidireccional, una solicitud pendiente
// enviada por fromAccountID hacia el dueño de accepterCharacterID.
func (s *Service) Accept(accepterCharacterID, fromAccountID string) (accepterAccountID string, err error) {
	accepterAccountID, err = s.accountIDForCharacter(accepterCharacterID)
	if err != nil {
		return "", err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`UPDATE friendships SET status = 'accepted', accepted_at = now()
		 WHERE account_id = $1 AND friend_id = $2 AND status = 'pending'`,
		fromAccountID, accepterAccountID,
	)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrRequestNotFound
	}

	_, err = tx.Exec(
		`INSERT INTO friendships (account_id, friend_id, status, accepted_at) VALUES ($1, $2, 'accepted', now())
		 ON CONFLICT (account_id, friend_id) DO UPDATE SET status = 'accepted', accepted_at = now()`,
		accepterAccountID, fromAccountID,
	)
	if err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return accepterAccountID, nil
}

// Decline borra una solicitud pendiente sin crear amistad.
func (s *Service) Decline(declinerCharacterID, fromAccountID string) error {
	declinerAccountID, err := s.accountIDForCharacter(declinerCharacterID)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(
		`DELETE FROM friendships WHERE account_id = $1 AND friend_id = $2 AND status = 'pending'`,
		fromAccountID, declinerAccountID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrRequestNotFound
	}
	return nil
}

// Remove elimina la amistad (o solicitud pendiente) en ambas direcciones.
func (s *Service) Remove(characterID, targetAccountID string) (accountID string, err error) {
	accountID, err = s.accountIDForCharacter(characterID)
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(
		`DELETE FROM friendships WHERE (account_id = $1 AND friend_id = $2) OR (account_id = $2 AND friend_id = $1)`,
		accountID, targetAccountID,
	)
	return accountID, err
}

type FriendEntry struct {
	AccountID   string
	Username    string
	CharacterID string // personaje representativo de esa cuenta, puede venir vacío
}

// List devuelve los amigos aceptados de la cuenta dueña de characterID.
func (s *Service) List(characterID string) ([]FriendEntry, error) {
	accountID, err := s.accountIDForCharacter(characterID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(
		`SELECT a.id, a.username,
		        COALESCE((SELECT c.id::text FROM characters c WHERE c.account_id = a.id ORDER BY c.created_at ASC LIMIT 1), '')
		 FROM friendships f
		 JOIN accounts a ON a.id = f.friend_id
		 WHERE f.account_id = $1 AND f.status = 'accepted'`,
		accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FriendEntry
	for rows.Next() {
		var e FriendEntry
		if err := rows.Scan(&e.AccountID, &e.Username, &e.CharacterID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
