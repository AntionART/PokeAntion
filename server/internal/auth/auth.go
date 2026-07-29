package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"

	"pokemon-online/server/internal/email"
)

var (
	ErrInvalidCredentials = errors.New("credenciales inválidas")
	ErrUsernameTaken      = errors.New("nombre de usuario ya existe")
	ErrInvalidToken       = errors.New("session_token inválido o expirado")
	ErrInvalidVerifyToken = errors.New("token de verificación de email inválido")
	ErrInvalidResetToken  = errors.New("token de recuperación de contraseña inválido o expirado")
)

// tokenTTL es la vigencia del JWT emitido en login_ok. El cliente lo reutiliza
// para reconectar el WebSocket (ej. tras perder la conexión) sin volver a pedir
// usuario/contraseña; cada reconexión emite un token nuevo con TTL renovado.
const tokenTTL = 24 * time.Hour

// resetTokenTTL: a diferencia del token de verificación de email (que no vence — un link viejo
// simplemente confirma la cuenta más tarde, sin riesgo real), un link de recuperación de
// contraseña vivo indefinidamente SÍ es un riesgo real si se filtra o queda en un correo viejo
// reenviado sin querer — vence en 1 hora, forzando a pedir uno nuevo si no se usó a tiempo.
const resetTokenTTL = 1 * time.Hour

type Service struct {
	db        *sql.DB
	jwtSecret []byte
	email     email.Sender
	publicURL string
}

// NewService recibe emailSender/publicURL para armar y mandar el correo de verificación al
// registrar (ver Register) — emailSender puede ser email.ConsoleSender{} en un entorno sin SMTP
// configurado, el registro nunca depende de que el envío real funcione.
func NewService(db *sql.DB, jwtSecret string, emailSender email.Sender, publicURL string) *Service {
	return &Service{db: db, jwtSecret: []byte(jwtSecret), email: emailSender, publicURL: publicURL}
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
func (s *Service) Register(username, userEmail, password, romID, nickname string) (AuthResult, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return AuthResult{}, fmt.Errorf("hasheando contraseña: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return AuthResult{}, err
	}
	defer tx.Rollback()

	verifyToken, err := randomToken()
	if err != nil {
		return AuthResult{}, fmt.Errorf("generando token de verificación: %w", err)
	}

	accountID := uuid.NewString()
	_, err = tx.Exec(
		`INSERT INTO accounts (id, username, email, password_hash, email_verify_token, email_verify_sent_at)
		 VALUES ($1, $2, $3, $4, $5, now())`,
		accountID, username, userEmail, string(hash), verifyToken,
	)
	if err != nil {
		// Código 23505 de Postgres = violación de unique constraint — accounts.username es
		// UNIQUE, así que esto es casi siempre "ese usuario ya existe", no un error genérico de
		// base de datos; distinguirlo deja que el llamador (HTTP /register) devuelva un mensaje
		// claro en vez de un 409 opaco para cualquier fallo.
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return AuthResult{}, ErrUsernameTaken
		}
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

	// El correo se manda DESPUÉS del commit, fuera de la transacción: si el envío falla (SMTP
	// caído, credenciales mal puestas), la cuenta ya quedó creada igual — verificar el email es
	// una mejora opcional, nunca debe poder tumbar el registro. Se loguea el error nada más.
	verifyLink := fmt.Sprintf("%s/verify-email?token=%s", s.publicURL, verifyToken)
	body := fmt.Sprintf("¡Bienvenido a Pokémon Online, %s!\n\nConfirmá tu cuenta entrando a este link:\n%s\n\nSi no creaste esta cuenta, ignorá este correo.", username, verifyLink)
	if err := s.email.Send(userEmail, "Confirmá tu cuenta de Pokémon Online", body); err != nil {
		slog.Warn("no se pudo mandar el correo de verificación", "component", "auth", "account_id", accountID, "error", err)
	}

	return AuthResult{
		AccountID: accountID, CharacterID: characterID, Nickname: nickname,
		MapID: startMap, PosX: startX, PosY: startY, Color: "default",
		Money: 3000, StarterSpecies: 0,
	}, nil
}

// randomToken genera un token de verificación de 32 bytes (64 caracteres hex) — suficiente
// entropía para que adivinarlo sea inviable, mismo criterio de tamaño que un session_token.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// VerifyEmail marca la cuenta dueña de token como verificada. No falla "en silencio" con un
// token repetido: si ya se usó (o nunca existió), simplemente no encuentra ninguna fila y
// devuelve ErrInvalidVerifyToken — el link de verificación es de un solo uso.
func (s *Service) VerifyEmail(token string) error {
	res, err := s.db.Exec(
		`UPDATE accounts SET email_verified = true, email_verify_token = NULL
		 WHERE email_verify_token = $1 AND email_verified = false`,
		token,
	)
	if err != nil {
		return fmt.Errorf("verificando email: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrInvalidVerifyToken
	}
	return nil
}

// RequestPasswordReset genera un token de recuperación y manda el link por correo si userEmail
// pertenece a una cuenta real. Nunca devuelve un error distinguible por "esa cuenta no existe" —
// el llamador (HTTP /request-password-reset) siempre responde igual haya o no una cuenta con
// ese email, para no dejar enumerar cuentas registradas probando emails al azar. El error
// devuelto acá es solo para fallos reales de infraestructura (DB caída), no "no encontrado".
func (s *Service) RequestPasswordReset(userEmail string) error {
	var accountID string
	err := s.db.QueryRow(`SELECT id FROM accounts WHERE email = $1`, userEmail).Scan(&accountID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("buscando cuenta: %w", err)
	}

	resetToken, err := randomToken()
	if err != nil {
		return fmt.Errorf("generando token de recuperación: %w", err)
	}

	_, err = s.db.Exec(
		`UPDATE accounts SET password_reset_token = $1, password_reset_sent_at = now() WHERE id = $2`,
		resetToken, accountID,
	)
	if err != nil {
		return fmt.Errorf("guardando token de recuperación: %w", err)
	}

	resetLink := fmt.Sprintf("%s/reset-password?token=%s", s.publicURL, resetToken)
	body := fmt.Sprintf("Pediste recuperar tu contraseña de Pokémon Online.\n\nEntrá a este link para elegir una nueva (vence en 1 hora):\n%s\n\nSi no pediste esto, ignorá este correo — tu contraseña actual sigue funcionando igual.", resetLink)
	if err := s.email.Send(userEmail, "Recuperar tu contraseña de Pokémon Online", body); err != nil {
		slog.Warn("no se pudo mandar el correo de recuperación de contraseña", "component", "auth", "account_id", accountID, "error", err)
	}
	return nil
}

// ResetPassword valida el token (existe Y no venció) y reemplaza password_hash. Token de un
// solo uso: se limpia apenas se consume, tanto si tiene éxito como si la cuenta que lo tenía
// ya no matchea (no debería pasar nunca, pero limpiar igual es más seguro que dejarlo colgado).
func (s *Service) ResetPassword(token, newPassword string) error {
	var accountID string
	var sentAt time.Time
	err := s.db.QueryRow(
		`SELECT id, password_reset_sent_at FROM accounts WHERE password_reset_token = $1`, token,
	).Scan(&accountID, &sentAt)
	if err == sql.ErrNoRows {
		return ErrInvalidResetToken
	}
	if err != nil {
		return fmt.Errorf("buscando token de recuperación: %w", err)
	}
	if time.Since(sentAt) > resetTokenTTL {
		return ErrInvalidResetToken
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hasheando contraseña nueva: %w", err)
	}

	_, err = s.db.Exec(
		`UPDATE accounts SET password_hash = $1, password_reset_token = NULL, password_reset_sent_at = NULL WHERE id = $2`,
		string(hash), accountID,
	)
	if err != nil {
		return fmt.Errorf("actualizando contraseña: %w", err)
	}
	return nil
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
