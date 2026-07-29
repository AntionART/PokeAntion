package auth

import (
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"pokemon-online/server/internal/email"
)

// Tests de integración reales contra Postgres (no mocks) — mismo criterio que
// server/internal/trade/trade_test.go: auth es la puerta de entrada literal del producto
// (nada más funciona si registrarse/loguearse no funciona), y no tenía ningún test hasta ahora.

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://pokemon:pokemon@localhost:5432/pokemon_online_test?sslmode=disable"
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Skipf("no se pudo abrir la base de test (%s): %v", url, err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("no se pudo conectar a la base de test (%s): %v —¿está levantada?", url, err)
	}
	return db
}

func cleanupAccount(t *testing.T, db *sql.DB, accountID string) {
	t.Helper()
	if _, err := db.Exec(`DELETE FROM accounts WHERE id = $1`, accountID); err != nil {
		t.Errorf("limpiando cuenta de test %s: %v", accountID, err)
	}
}

func TestRegister_CreatesAccountAndCharacter(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	svc := NewService(db, "test-jwt-secret", email.ConsoleSender{}, "http://localhost:8080")

	username := "at_" + uuid.NewString()[:12]
	result, err := svc.Register(username, username+"@test.local", "hunter2pass", "emerald_es", "TestNick")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer cleanupAccount(t, db, result.AccountID)

	if result.AccountID == "" || result.CharacterID == "" {
		t.Fatalf("Register no devolvió IDs: %+v", result)
	}
	if result.Money != 3000 {
		t.Fatalf("dinero inicial inesperado: %d (esperaba 3000)", result.Money)
	}

	// La contraseña nunca debe quedar en texto plano — confirmar que lo guardado es un hash
	// bcrypt real (empieza con "$2"), no la contraseña cruda.
	var storedHash string
	if err := db.QueryRow(`SELECT password_hash FROM accounts WHERE id = $1`, result.AccountID).Scan(&storedHash); err != nil {
		t.Fatalf("consultando password_hash: %v", err)
	}
	if storedHash == "hunter2pass" {
		t.Fatal("la contraseña se guardó en texto plano, no hasheada")
	}
	if len(storedHash) < 20 || storedHash[:2] != "$2" {
		t.Fatalf("password_hash no parece un hash bcrypt real: %q", storedHash)
	}
}

func TestRegister_DuplicateUsername(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	svc := NewService(db, "test-jwt-secret", email.ConsoleSender{}, "http://localhost:8080")

	username := "atd_" + uuid.NewString()[:12]
	first, err := svc.Register(username, username+"@test.local", "pass12345", "emerald_es", "First")
	if err != nil {
		t.Fatalf("primer Register: %v", err)
	}
	defer cleanupAccount(t, db, first.AccountID)

	_, err = svc.Register(username, "otro-email@test.local", "otraPass", "emerald_es", "Second")
	if err != ErrUsernameTaken {
		t.Fatalf("esperaba ErrUsernameTaken registrando un username repetido, dio: %v", err)
	}
}

func TestLogin_ValidAndInvalidCredentials(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	svc := NewService(db, "test-jwt-secret", email.ConsoleSender{}, "http://localhost:8080")

	username := "atl_" + uuid.NewString()[:12]
	reg, err := svc.Register(username, username+"@test.local", "correctPass1", "emerald_es", "LoginTest")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer cleanupAccount(t, db, reg.AccountID)

	t.Run("credenciales correctas", func(t *testing.T) {
		result, err := svc.Login(username, "correctPass1")
		if err != nil {
			t.Fatalf("Login con credenciales correctas falló: %v", err)
		}
		if result.CharacterID != reg.CharacterID {
			t.Fatalf("Login devolvió un character_id distinto al de Register: %s vs %s", result.CharacterID, reg.CharacterID)
		}
	})

	t.Run("contraseña incorrecta", func(t *testing.T) {
		if _, err := svc.Login(username, "contraseñaMala"); err != ErrInvalidCredentials {
			t.Fatalf("esperaba ErrInvalidCredentials con contraseña incorrecta, dio: %v", err)
		}
	})

	t.Run("usuario inexistente", func(t *testing.T) {
		if _, err := svc.Login("usuario_que_no_existe_"+uuid.NewString(), "cualquiera"); err != ErrInvalidCredentials {
			t.Fatalf("esperaba ErrInvalidCredentials con usuario inexistente, dio: %v", err)
		}
	})
}

func TestTokenRoundTrip(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	svc := NewService(db, "test-jwt-secret", email.ConsoleSender{}, "http://localhost:8080")

	username := "att_" + uuid.NewString()[:12]
	reg, err := svc.Register(username, username+"@test.local", "tokenPass1", "emerald_es", "TokenTest")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer cleanupAccount(t, db, reg.AccountID)

	token, err := svc.IssueToken(reg.AccountID, reg.CharacterID)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	t.Run("ValidateToken devuelve las identidades correctas", func(t *testing.T) {
		accountID, characterID, err := svc.ValidateToken(token)
		if err != nil {
			t.Fatalf("ValidateToken: %v", err)
		}
		if accountID != reg.AccountID || characterID != reg.CharacterID {
			t.Fatalf("ValidateToken devolvió identidades inesperadas: %s/%s", accountID, characterID)
		}
	})

	t.Run("LoginWithToken reautentica sin usuario/contraseña", func(t *testing.T) {
		result, err := svc.LoginWithToken(token)
		if err != nil {
			t.Fatalf("LoginWithToken: %v", err)
		}
		if result.CharacterID != reg.CharacterID {
			t.Fatalf("LoginWithToken devolvió character_id inesperado: %s", result.CharacterID)
		}
	})

	t.Run("un token con otro secreto se rechaza", func(t *testing.T) {
		otherSvc := NewService(db, "un-secreto-distinto", email.ConsoleSender{}, "http://localhost:8080")
		if _, _, err := otherSvc.ValidateToken(token); err != ErrInvalidToken {
			t.Fatalf("esperaba ErrInvalidToken con un secreto distinto, dio: %v", err)
		}
	})

	t.Run("un token vencido se rechaza", func(t *testing.T) {
		claims := sessionClaims{
			AccountID: reg.AccountID, CharacterID: reg.CharacterID,
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			},
		}
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(svc.jwtSecret)
		if err != nil {
			t.Fatalf("firmando token vencido de test: %v", err)
		}
		if _, _, err := svc.ValidateToken(raw); err != ErrInvalidToken {
			t.Fatalf("esperaba ErrInvalidToken con un token vencido, dio: %v", err)
		}
	})
}

// fakeEmailSender captura el último correo mandado, para poder extraer el link de
// verificación de test sin depender de SMTP real — mismo criterio que las demás pruebas de
// este proyecto (nunca mocks de librerías externas, pero acá no hay comportamiento real que
// probar del lado del proveedor de correo, solo QUÉ le mandamos).
type fakeEmailSender struct {
	to, subject, body string
}

func (f *fakeEmailSender) Send(to, subject, body string) error {
	f.to, f.subject, f.body = to, subject, body
	return nil
}

func TestRegister_SendsVerificationEmailWithRealToken(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	sender := &fakeEmailSender{}
	svc := NewService(db, "test-jwt-secret", sender, "http://localhost:8080")

	username := "atv_" + uuid.NewString()[:12]
	testEmail := username + "@test.local"
	result, err := svc.Register(username, testEmail, "hunter2pass", "emerald_es", "TestNick")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer cleanupAccount(t, db, result.AccountID)

	if sender.to != testEmail {
		t.Errorf("email mandado a %q, esperaba %q", sender.to, testEmail)
	}
	if !strings.Contains(sender.body, "http://localhost:8080/verify-email?token=") {
		t.Errorf("cuerpo del correo no incluye el link de verificación esperado: %q", sender.body)
	}

	var verified bool
	var tokenInDB sql.NullString
	if err := db.QueryRow(`SELECT email_verified, email_verify_token FROM accounts WHERE id = $1`, result.AccountID).Scan(&verified, &tokenInDB); err != nil {
		t.Fatalf("consultando cuenta: %v", err)
	}
	if verified {
		t.Errorf("email_verified = true recién registrado, esperaba false")
	}
	if !tokenInDB.Valid || !strings.Contains(sender.body, tokenInDB.String) {
		t.Errorf("el token guardado en la base (%v) no aparece en el correo mandado", tokenInDB)
	}
}

func TestVerifyEmail_MarksAccountVerifiedAndConsumesToken(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	sender := &fakeEmailSender{}
	svc := NewService(db, "test-jwt-secret", sender, "http://localhost:8080")

	username := "atv2_" + uuid.NewString()[:12]
	result, err := svc.Register(username, username+"@test.local", "hunter2pass", "emerald_es", "TestNick")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer cleanupAccount(t, db, result.AccountID)

	var token string
	if err := db.QueryRow(`SELECT email_verify_token FROM accounts WHERE id = $1`, result.AccountID).Scan(&token); err != nil {
		t.Fatalf("consultando token: %v", err)
	}

	if err := svc.VerifyEmail(token); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}

	var verified bool
	var tokenAfter sql.NullString
	if err := db.QueryRow(`SELECT email_verified, email_verify_token FROM accounts WHERE id = $1`, result.AccountID).Scan(&verified, &tokenAfter); err != nil {
		t.Fatalf("consultando cuenta tras verificar: %v", err)
	}
	if !verified {
		t.Errorf("email_verified = false tras VerifyEmail, esperaba true")
	}
	if tokenAfter.Valid {
		t.Errorf("email_verify_token = %q tras verificar, esperaba NULL (token de un solo uso)", tokenAfter.String)
	}

	// Reusar el mismo token (ej. alguien hace doble click en el link) debe rechazarse, no
	// verificar "de nuevo" en silencio.
	if err := svc.VerifyEmail(token); err != ErrInvalidVerifyToken {
		t.Errorf("segundo VerifyEmail con el mismo token = %v, esperaba ErrInvalidVerifyToken", err)
	}
}

func TestVerifyEmail_RejectsUnknownToken(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	svc := NewService(db, "test-jwt-secret", email.ConsoleSender{}, "http://localhost:8080")

	if err := svc.VerifyEmail("un-token-que-nunca-existio"); err != ErrInvalidVerifyToken {
		t.Errorf("VerifyEmail con token inexistente = %v, esperaba ErrInvalidVerifyToken", err)
	}
}

func TestRequestPasswordReset_SendsEmailWithRealToken(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	sender := &fakeEmailSender{}
	svc := NewService(db, "test-jwt-secret", sender, "http://localhost:8080")

	username := "apr_" + uuid.NewString()[:12]
	testEmail := username + "@test.local"
	result, err := svc.Register(username, testEmail, "hunter2pass", "emerald_es", "TestNick")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer cleanupAccount(t, db, result.AccountID)

	if err := svc.RequestPasswordReset(testEmail); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}

	if sender.to != testEmail {
		t.Errorf("email mandado a %q, esperaba %q", sender.to, testEmail)
	}
	if !strings.Contains(sender.body, "http://localhost:8080/reset-password?token=") {
		t.Errorf("cuerpo del correo no incluye el link de recuperación esperado: %q", sender.body)
	}

	var tokenInDB sql.NullString
	if err := db.QueryRow(`SELECT password_reset_token FROM accounts WHERE id = $1`, result.AccountID).Scan(&tokenInDB); err != nil {
		t.Fatalf("consultando token: %v", err)
	}
	if !tokenInDB.Valid || !strings.Contains(sender.body, tokenInDB.String) {
		t.Errorf("el token guardado en la base (%v) no aparece en el correo mandado", tokenInDB)
	}
}

// Ni error ni correo mandado con un email que no existe — ver el comentario de
// RequestPasswordReset sobre por qué esto NO se distingue del caso real (evitar enumeración
// de cuentas registradas).
func TestRequestPasswordReset_UnknownEmailDoesNotErrorOrSend(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	sender := &fakeEmailSender{}
	svc := NewService(db, "test-jwt-secret", sender, "http://localhost:8080")

	if err := svc.RequestPasswordReset("nadie_"+uuid.NewString()+"@test.local"); err != nil {
		t.Fatalf("RequestPasswordReset con email inexistente devolvió error: %v (esperaba nil)", err)
	}
	if sender.to != "" {
		t.Errorf("se mandó un correo (%q) para un email que no corresponde a ninguna cuenta", sender.to)
	}
}

func TestResetPassword_ChangesPasswordAndConsumesToken(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	sender := &fakeEmailSender{}
	svc := NewService(db, "test-jwt-secret", sender, "http://localhost:8080")

	username := "arp_" + uuid.NewString()[:12]
	testEmail := username + "@test.local"
	result, err := svc.Register(username, testEmail, "oldPass123", "emerald_es", "TestNick")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer cleanupAccount(t, db, result.AccountID)

	if err := svc.RequestPasswordReset(testEmail); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	var token string
	if err := db.QueryRow(`SELECT password_reset_token FROM accounts WHERE id = $1`, result.AccountID).Scan(&token); err != nil {
		t.Fatalf("consultando token: %v", err)
	}

	if err := svc.ResetPassword(token, "newPass456"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	// La contraseña vieja ya no sirve, la nueva sí.
	if _, err := svc.Login(username, "oldPass123"); err != ErrInvalidCredentials {
		t.Errorf("Login con la contraseña VIEJA tras el reset = %v, esperaba ErrInvalidCredentials", err)
	}
	if _, err := svc.Login(username, "newPass456"); err != nil {
		t.Errorf("Login con la contraseña NUEVA tras el reset falló: %v", err)
	}

	var tokenAfter sql.NullString
	if err := db.QueryRow(`SELECT password_reset_token FROM accounts WHERE id = $1`, result.AccountID).Scan(&tokenAfter); err != nil {
		t.Fatalf("consultando token tras reset: %v", err)
	}
	if tokenAfter.Valid {
		t.Errorf("password_reset_token = %q tras ResetPassword, esperaba NULL (token de un solo uso)", tokenAfter.String)
	}

	// Reusar el mismo token debe rechazarse, no resetear "de nuevo" en silencio.
	if err := svc.ResetPassword(token, "otraPassMas"); err != ErrInvalidResetToken {
		t.Errorf("segundo ResetPassword con el mismo token = %v, esperaba ErrInvalidResetToken", err)
	}
}

func TestResetPassword_RejectsUnknownToken(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	svc := NewService(db, "test-jwt-secret", email.ConsoleSender{}, "http://localhost:8080")

	if err := svc.ResetPassword("un-token-que-nunca-existio", "cualquierPass"); err != ErrInvalidResetToken {
		t.Errorf("ResetPassword con token inexistente = %v, esperaba ErrInvalidResetToken", err)
	}
}

func TestResetPassword_RejectsExpiredToken(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	svc := NewService(db, "test-jwt-secret", email.ConsoleSender{}, "http://localhost:8080")

	username := "arpe_" + uuid.NewString()[:12]
	testEmail := username + "@test.local"
	result, err := svc.Register(username, testEmail, "hunter2pass", "emerald_es", "TestNick")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer cleanupAccount(t, db, result.AccountID)

	if err := svc.RequestPasswordReset(testEmail); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	var token string
	if err := db.QueryRow(`SELECT password_reset_token FROM accounts WHERE id = $1`, result.AccountID).Scan(&token); err != nil {
		t.Fatalf("consultando token: %v", err)
	}

	// Simular que el token se pidió hace 2 horas (venció hace 1 hora) sin tener que esperar de
	// verdad — mismo criterio que TestTokenRoundTrip con JWT vencidos a mano.
	if _, err := db.Exec(`UPDATE accounts SET password_reset_sent_at = now() - interval '2 hours' WHERE id = $1`, result.AccountID); err != nil {
		t.Fatalf("forzando vencimiento del token: %v", err)
	}

	if err := svc.ResetPassword(token, "cualquierPass"); err != ErrInvalidResetToken {
		t.Errorf("ResetPassword con token vencido = %v, esperaba ErrInvalidResetToken", err)
	}
}
