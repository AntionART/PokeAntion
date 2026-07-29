package character

import (
	"database/sql"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// Tests de integración reales contra Postgres, mismo criterio que internal/market: esto mueve
// dinero real, así que lo que preocupa es la condición de carrera de dos compras concurrentes
// (ver TryDebitMoney), no algo que un mock pudiera probar.

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
		t.Skipf("no se pudo conectar a la base de test (%s): %v", url, err)
	}
	return db
}

func createCharacter(t *testing.T, db *sql.DB, username string, money int) (characterID, accountID string) {
	t.Helper()
	accountID = uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO accounts (id, username, email, password_hash) VALUES ($1, $2, $3, 'x')`,
		accountID, username, username+"@test.local",
	); err != nil {
		t.Fatalf("creando cuenta: %v", err)
	}

	characterID = uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO characters (id, account_id, rom_id, nickname, map_id, money) VALUES ($1, $2, 'emerald_es', $3, 'test_map', $4)`,
		characterID, accountID, username, money,
	); err != nil {
		t.Fatalf("creando personaje: %v", err)
	}
	return characterID, accountID
}

func cleanupAccount(t *testing.T, db *sql.DB, accountID string) {
	t.Helper()
	if _, err := db.Exec(`DELETE FROM accounts WHERE id = $1`, accountID); err != nil {
		t.Errorf("limpiando cuenta %s: %v", accountID, err)
	}
}

func moneyOf(t *testing.T, db *sql.DB, characterID string) int {
	t.Helper()
	var money int
	if err := db.QueryRow(`SELECT money FROM characters WHERE id = $1`, characterID).Scan(&money); err != nil {
		t.Fatalf("consultando money de %s: %v", characterID, err)
	}
	return money
}

func TestTryDebitMoney_SucceedsAndPersists(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	charID, accID := createCharacter(t, db, "chartest_debit_ok", 1000)
	defer cleanupAccount(t, db, accID)

	svc := NewService(db)
	newMoney, err := svc.TryDebitMoney(charID, 300)
	if err != nil {
		t.Fatalf("TryDebitMoney: %v", err)
	}
	if newMoney != 700 {
		t.Errorf("newMoney = %d, esperaba 700", newMoney)
	}
	if got := moneyOf(t, db, charID); got != 700 {
		t.Errorf("money persistido = %d, esperaba 700", got)
	}
}

func TestTryDebitMoney_InsufficientFunds(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	charID, accID := createCharacter(t, db, "chartest_debit_poor", 100)
	defer cleanupAccount(t, db, accID)

	svc := NewService(db)
	if _, err := svc.TryDebitMoney(charID, 300); err != ErrInsufficientFunds {
		t.Fatalf("err = %v, esperaba ErrInsufficientFunds", err)
	}
	// Plata intacta: un intento fallido no debe descontar nada.
	if got := moneyOf(t, db, charID); got != 100 {
		t.Errorf("money tras fallo = %d, esperaba 100 (sin cambios)", got)
	}
}

// TestTryDebitMoney_ConcurrentNeverGoesNegative es la razón real de que TryDebitMoney sea un
// solo UPDATE...WHERE en vez de un SELECT seguido de un UPDATE separado: dos compras
// concurrentes por el mismo personaje, cada una alcanzando el dinero sola pero no las dos
// juntas, no deben poder dejarlo en negativo (double-spend por condición de carrera).
func TestTryDebitMoney_ConcurrentNeverGoesNegative(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	charID, accID := createCharacter(t, db, "chartest_debit_race", 100)
	defer cleanupAccount(t, db, accID)

	svc := NewService(db)
	const attempts = 10
	const cost = 60 // solo alcanza para 1 de los 10 intentos concurrentes (100/60 = 1)

	var wg sync.WaitGroup
	successes := make([]bool, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.TryDebitMoney(charID, cost)
			successes[idx] = err == nil
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, ok := range successes {
		if ok {
			successCount++
		}
	}
	if successCount != 1 {
		t.Errorf("compras exitosas concurrentes = %d, esperaba exactamente 1 (100/%d)", successCount, cost)
	}
	if got := moneyOf(t, db, charID); got != 100-cost {
		t.Errorf("money final = %d, esperaba %d (nunca negativo)", got, 100-cost)
	}
}
