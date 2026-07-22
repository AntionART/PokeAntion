package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("credenciales inválidas")
	ErrUsernameTaken      = errors.New("nombre de usuario ya existe")
	ErrInvalidToken       = errors.New("session_token inválido o expirado")
)

// tokenTTL es la vigencia del JWT emitido en login_ok. El cliente lo reutiliza
// para reconectar el WebSocket (ej. tras perder la conexión) sin volver a pedir
// usuario/contraseña; cada reconexión emite un token nuevo con TTL renovado.
const tokenTTL = 24 * time.Hour

type Service struct {
	db        *sql.DB
	jwtSecret []byte
}

func NewService(db *sql.DB, jwtSecret string) *Service {
	return &Service{db: db, jwtSecret: []byte(jwtSecret)}
}

type AuthResult struct {
	AccountID   string `json:"account_id"`
	CharacterID string `json:"character_id"`
	Nickname    string `json:"nickname"`
	MapID       string `json:"map_id"`
	PosX        int    `json:"pos_x"`
	PosY        int    `json:"pos_y"`
	Color       string `json:"color"`
	Money       int    `json:"money"`
	// StarterSpecies es el species del Pokémon en el slot 0 del equipo (tabla `pokemon`, ver
	// paquete internal/pokemon), o 0 (SPECIES_NONE) si todavía no tiene ninguno — derivado, no
	// es una columna propia (evita tener dos fuentes de verdad para lo mismo).
	StarterSpecies int `json:"starter_species"`
}

type sessionClaims struct {
	AccountID   string `json:"account_id"`
	CharacterID string `json:"character_id"`
	jwt.RegisteredClaims
}

// IssueToken firma un JWT de sesión para el personaje ya autenticado. Se emite
// en cada login exitoso (por password o por token) para que la vigencia se renueve.
func (s *Service) IssueToken(accountID, characterID string) (string, error) {
	claims := sessionClaims{
		AccountID:   accountID,
		CharacterID: characterID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

// ValidateToken verifica firma y expiración de un session_token y devuelve
// las identidades que lleva incrustadas.
func (s *Service) ValidateToken(tokenString string) (accountID, characterID string, err error) {
	claims := &sessionClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("método de firma inesperado: %v", t.Header["alg"])
		}
		return s.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return "", "", ErrInvalidToken
	}
	return claims.AccountID, claims.CharacterID, nil
}

// Register crea la cuenta y su primer personaje para la ROM indicada.
// Nunca guarda la contraseña en texto plano: se hashea con bcrypt antes de tocar la base de datos.
func (s *Service) Register(username, email, password, romID, nickname string) (AuthResult, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return AuthResult{}, fmt.Errorf("hasheando contraseña: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return AuthResult{}, err
	}
	defer tx.Rollback()

	accountID := uuid.NewString()
	_, err = tx.Exec(
		`INSERT INTO accounts (id, username, email, password_hash) VALUES ($1, $2, $3, $4)`,
		accountID, username, email, string(hash),
	)
	if err != nil {
		// En Postgres real, distinguir violación de unique constraint (código 23505)
		// para devolver ErrUsernameTaken en vez de un error genérico.
		return AuthResult{}, fmt.Errorf("creando cuenta: %w", err)
	}

	startMap, startX, startY := spawnPointFor(tx, romID)

	characterID := uuid.NewString()
	_, err = tx.Exec(
		`INSERT INTO characters (id, account_id, rom_id, nickname, map_id, pos_x, pos_y)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		characterID, accountID, romID, nickname, startMap, startX, startY,
	)
	if err != nil {
		return AuthResult{}, fmt.Errorf("creando personaje: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return AuthResult{}, err
	}

	return AuthResult{
		AccountID: accountID, CharacterID: characterID, Nickname: nickname,
		MapID: startMap, PosX: startX, PosY: startY, Color: "default",
		Money: 3000, StarterSpecies: 0,
	}, nil
}

// spawnPointFor busca dónde nace un personaje nuevo de romID en rom_spawn_points — dato
// específico de cada ROM (Villa Raíz para Esmeralda, otro pueblo para otra ROM), tan opaco
// para el servidor como species_id: se agrega soporte a una ROM nueva con un INSERT en esa
// tabla, no tocando este código. Si no hay fila para esa ROM (todavía no se cargó su spawn),
// no se rechaza el registro — se usa un fallback neutro y se loguea la falta, porque el cliente
// va a corregir la posición real apenas RomLoader pueda leerla (ver Fase RomLoader-2/3).
func spawnPointFor(tx *sql.Tx, romID string) (mapID string, x, y int) {
	err := tx.QueryRow(
		`SELECT map_id, pos_x, pos_y FROM rom_spawn_points WHERE rom_id = $1`, romID,
	).Scan(&mapID, &x, &y)
	if err == nil {
		return mapID, x, y
	}
	slog.Warn("no hay spawn point configurado para esta ROM, usando fallback neutro",
		"component", "auth", "rom_id", romID)
	return "unknown", 0, 0
}

// Login valida usuario/contraseña y devuelve el estado inicial del personaje activo.
func (s *Service) Login(username, password string) (AuthResult, error) {
	var accountID, passwordHash string
	err := s.db.QueryRow(
		`SELECT id, password_hash FROM accounts WHERE username = $1 AND is_banned = false`,
		username,
	).Scan(&accountID, &passwordHash)
	if err == sql.ErrNoRows {
		return AuthResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return AuthResult{}, fmt.Errorf("consultando cuenta: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return AuthResult{}, ErrInvalidCredentials
	}

	var characterID, nickname, mapID, color string
	var posX, posY, money, starterSpecies int
	err = s.db.QueryRow(
		`SELECT id, nickname, map_id, pos_x, pos_y, sprite_color, money,
		        COALESCE((SELECT species_id FROM pokemon WHERE owner_char_id = characters.id AND location = 'team' AND team_slot = 0), 0)
		 FROM characters WHERE account_id = $1 LIMIT 1`,
		accountID,
	).Scan(&characterID, &nickname, &mapID, &posX, &posY, &color, &money, &starterSpecies)
	if err != nil {
		return AuthResult{}, fmt.Errorf("cargando personaje: %w", err)
	}

	_, _ = s.db.Exec(`UPDATE accounts SET last_login_at = now() WHERE id = $1`, accountID)

	return AuthResult{
		AccountID: accountID, CharacterID: characterID, Nickname: nickname,
		MapID: mapID, PosX: posX, PosY: posY, Color: color,
		Money: money, StarterSpecies: starterSpecies,
	}, nil
}

// LoginWithToken reautentica una sesión a partir de un session_token JWT ya emitido
// (típicamente al reconectar el WebSocket sin volver a pedir usuario/contraseña).
func (s *Service) LoginWithToken(tokenString string) (AuthResult, error) {
	accountID, characterID, err := s.ValidateToken(tokenString)
	if err != nil {
		return AuthResult{}, err
	}

	var nickname, mapID, color string
	var posX, posY, money, starterSpecies int
	err = s.db.QueryRow(
		`SELECT nickname, map_id, pos_x, pos_y, sprite_color, money,
		        COALESCE((SELECT species_id FROM pokemon WHERE owner_char_id = characters.id AND location = 'team' AND team_slot = 0), 0)
		 FROM characters WHERE id = $1 AND account_id = $2`,
		characterID, accountID,
	).Scan(&nickname, &mapID, &posX, &posY, &color, &money, &starterSpecies)
	if err == sql.ErrNoRows {
		return AuthResult{}, ErrInvalidToken
	}
	if err != nil {
		return AuthResult{}, fmt.Errorf("cargando personaje: %w", err)
	}

	return AuthResult{
		AccountID: accountID, CharacterID: characterID, Nickname: nickname,
		MapID: mapID, PosX: posX, PosY: posY, Color: color,
		Money: money, StarterSpecies: starterSpecies,
	}, nil
}
