package social

import (
	"database/sql"
	"errors"
	"sync"

	"github.com/google/uuid"
)

var (
	ErrAlreadyInGuild   = errors.New("ya estás en un gremio")
	ErrGuildInviteNotFound = errors.New("no hay invitación pendiente de ese gremio")
	ErrNotGuildLeader   = errors.New("solo el líder del gremio puede invitar o expulsar")
	ErrGuildNotFound    = errors.New("gremio no encontrado")
	ErrGuildNameTaken   = errors.New("ya existe un gremio con ese nombre")
	ErrNotInGuild       = errors.New("no pertenecés a ningún gremio")
	ErrCannotKickSelf   = errors.New("usá 'salir del gremio' en vez de expulsarte a vos mismo")
)

// GuildService administra gremios (guilds/guild_members) — a diferencia de PartyService, son
// PERSISTENTES: no se disuelven solos al quedar todos desconectados, solo cuando el último
// miembro se va explícitamente. Las invitaciones pendientes, igual que en PartyService, viven
// en memoria (efímeras por naturaleza: si el destinatario se desconecta sin responder, tiene
// sentido que la invitación se pierda, no que quede colgada para siempre).
type GuildService struct {
	db *sql.DB

	mu      sync.Mutex
	invites map[string]string // targetCharacterID -> guildID pendiente de aceptar/declinar
}

func NewGuildService(db *sql.DB) *GuildService {
	return &GuildService{db: db, invites: make(map[string]string)}
}

func (s *GuildService) guildOfCharacter(characterID string) (string, error) {
	var guildID string
	err := s.db.QueryRow(`SELECT guild_id FROM guild_members WHERE char_id = $1`, characterID).Scan(&guildID)
	return guildID, err
}

// GuildOfCharacter expone la resolución characterID -> guildID (o "" si no está en ninguno)
// para que el router pueda enrutar el canal de chat "guild" sin duplicar esta query.
func (s *GuildService) GuildOfCharacter(characterID string) string {
	guildID, err := s.guildOfCharacter(characterID)
	if err != nil {
		return ""
	}
	return guildID
}

// Create funda un gremio nuevo con characterID como líder. Falla si el nombre ya existe o si
// characterID ya pertenece a un gremio.
func (s *GuildService) Create(characterID, name string) (guildID string, err error) {
	if _, err := s.guildOfCharacter(characterID); err == nil {
		return "", ErrAlreadyInGuild
	}

	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT count(*) FROM guilds WHERE name = $1`, name).Scan(&exists); err != nil {
		return "", err
	}
	if exists > 0 {
		return "", ErrGuildNameTaken
	}

	guildID = uuid.NewString()
	if _, err := tx.Exec(`INSERT INTO guilds (id, name, leader_char_id) VALUES ($1, $2, $3)`, guildID, name, characterID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`INSERT INTO guild_members (guild_id, char_id, is_officer) VALUES ($1, $2, TRUE)`, guildID, characterID); err != nil {
		return "", err
	}

	return guildID, tx.Commit()
}

// Invite registra una invitación pendiente en memoria. Solo el líder puede invitar (a
// diferencia de PartyService.Invite, acá no hay auto-creación: el gremio ya existe de antes).
func (s *GuildService) Invite(fromCharacterID, targetCharacterID string) (guildID string, err error) {
	guildID, err = s.guildOfCharacter(fromCharacterID)
	if err != nil {
		return "", ErrNotInGuild
	}
	var leaderID string
	if err := s.db.QueryRow(`SELECT leader_char_id FROM guilds WHERE id = $1`, guildID).Scan(&leaderID); err != nil {
		return "", ErrGuildNotFound
	}
	if leaderID != fromCharacterID {
		return "", ErrNotGuildLeader
	}
	if _, err := s.guildOfCharacter(targetCharacterID); err == nil {
		return "", ErrAlreadyInGuild
	}

	s.mu.Lock()
	s.invites[targetCharacterID] = guildID
	s.mu.Unlock()

	return guildID, nil
}

func (s *GuildService) Accept(characterID, guildID string) error {
	s.mu.Lock()
	pending, ok := s.invites[characterID]
	if ok {
		delete(s.invites, characterID)
	}
	s.mu.Unlock()
	if !ok || pending != guildID {
		return ErrGuildInviteNotFound
	}
	if _, err := s.guildOfCharacter(characterID); err == nil {
		return ErrAlreadyInGuild
	}

	_, err := s.db.Exec(`INSERT INTO guild_members (guild_id, char_id) VALUES ($1, $2)`, guildID, characterID)
	return err
}

func (s *GuildService) Decline(characterID, guildID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.invites[characterID]
	if !ok || pending != guildID {
		return ErrGuildInviteNotFound
	}
	delete(s.invites, characterID)
	return nil
}

// Leave saca a characterID de su gremio. Si era el líder y quedan más miembros, transfiere el
// liderazgo al miembro más antiguo restante (igual que PartyService.Leave); si era el último
// miembro, el gremio se DISUELVE (a diferencia de un party, esto no pasa "solo" nunca — un
// gremio con miembros desconectados sigue existiendo, únicamente se disuelve si alguien se va
// explícitamente siendo el último).
func (s *GuildService) Leave(characterID string) (guildID string, disbanded bool, err error) {
	guildID, err = s.guildOfCharacter(characterID)
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

	if _, err = tx.Exec(`DELETE FROM guild_members WHERE guild_id = $1 AND char_id = $2`, guildID, characterID); err != nil {
		return "", false, err
	}

	var remaining int
	if err = tx.QueryRow(`SELECT count(*) FROM guild_members WHERE guild_id = $1`, guildID).Scan(&remaining); err != nil {
		return "", false, err
	}

	if remaining == 0 {
		if _, err = tx.Exec(`DELETE FROM guilds WHERE id = $1`, guildID); err != nil {
			return "", false, err
		}
		disbanded = true
	} else {
		var leaderID string
		if err = tx.QueryRow(`SELECT leader_char_id FROM guilds WHERE id = $1`, guildID).Scan(&leaderID); err != nil {
			return "", false, err
		}
		if leaderID == characterID {
			var newLeader string
			if err = tx.QueryRow(`SELECT char_id FROM guild_members WHERE guild_id = $1 ORDER BY joined_at ASC LIMIT 1`, guildID).Scan(&newLeader); err != nil {
				return "", false, err
			}
			if _, err = tx.Exec(`UPDATE guilds SET leader_char_id = $1 WHERE id = $2`, newLeader, guildID); err != nil {
				return "", false, err
			}
			if _, err = tx.Exec(`UPDATE guild_members SET is_officer = TRUE WHERE guild_id = $1 AND char_id = $2`, guildID, newLeader); err != nil {
				return "", false, err
			}
		}
	}

	if err = tx.Commit(); err != nil {
		return "", false, err
	}
	return guildID, disbanded, nil
}

// Kick expulsa a targetCharacterID del gremio. Solo el líder puede hacerlo.
func (s *GuildService) Kick(leaderCharacterID, targetCharacterID string) (guildID string, err error) {
	if leaderCharacterID == targetCharacterID {
		return "", ErrCannotKickSelf
	}
	guildID, err = s.guildOfCharacter(leaderCharacterID)
	if err != nil {
		return "", ErrNotInGuild
	}
	var leaderID string
	if err := s.db.QueryRow(`SELECT leader_char_id FROM guilds WHERE id = $1`, guildID).Scan(&leaderID); err != nil {
		return "", ErrGuildNotFound
	}
	if leaderID != leaderCharacterID {
		return "", ErrNotGuildLeader
	}

	res, err := s.db.Exec(`DELETE FROM guild_members WHERE guild_id = $1 AND char_id = $2`, guildID, targetCharacterID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotInGuild
	}
	return guildID, nil
}

type GuildMember struct {
	CharacterID string
	Nickname    string
	IsOfficer   bool
}

// Members devuelve los miembros actuales de un gremio, con su nickname resuelto desde
// characters (no desde el Hub de websockets: a diferencia de party, un gremio tiene miembros
// DESCONECTADOS la mayor parte del tiempo — el Hub solo conoce a quien está conectado ahora
// mismo, así que resolver el nombre ahí dejaría a los offline sin nickname). "Online" sigue
// resolviéndose aparte, contra el Hub, en el router — eso sí es información en vivo legítima.
func (s *GuildService) Members(guildID string) ([]GuildMember, error) {
	rows, err := s.db.Query(`
		SELECT gm.char_id, c.nickname, gm.is_officer
		FROM guild_members gm
		JOIN characters c ON c.id = gm.char_id
		WHERE gm.guild_id = $1
		ORDER BY gm.joined_at ASC`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GuildMember
	for rows.Next() {
		var m GuildMember
		if err := rows.Scan(&m.CharacterID, &m.Nickname, &m.IsOfficer); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// NameOf devuelve el nombre de un gremio, para mostrarlo en la UI sin otra query aparte.
func (s *GuildService) NameOf(guildID string) (string, error) {
	var name string
	err := s.db.QueryRow(`SELECT name FROM guilds WHERE id = $1`, guildID).Scan(&name)
	return name, err
}

// LeaderOf devuelve el character_id del líder actual de un gremio.
func (s *GuildService) LeaderOf(guildID string) (string, error) {
	var leaderID string
	err := s.db.QueryRow(`SELECT leader_char_id FROM guilds WHERE id = $1`, guildID).Scan(&leaderID)
	return leaderID, err
}
