package social

import (
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// Tests de integración reales contra Postgres (no mocks), mismo criterio que
// internal/trade/trade_test.go: amigos/grupos/gremios tocan estado compartido real (filas de
// otras cuentas, transferencia de liderazgo, disolución) que un mock no ejercitaría de verdad.
//
// Si no hay conexión disponible a TEST_DATABASE_URL (ni al default), los tests se saltan (no
// fallan) para no romper `go build`/`go vet` en un entorno sin Postgres.

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
		t.Skipf("no se pudo conectar a la base de test (%s): %v —¿está levantada? Ver README.md sección de Postgres local.", url, err)
	}
	return db
}

type testFixture struct {
	db         *sql.DB
	accountIDs []string
}

func newFixture(db *sql.DB) *testFixture {
	return &testFixture{db: db}
}

// uniqueSuffix da 8 caracteres hex únicos por llamada. Los usernames son VARCHAR(20) — todo
// prefijo usado junto a esto debe quedar en <=12 caracteres para no violar esa columna (bug
// real ya encontrado una vez esta sesión con los tests de auth: un prefijo largo + sufijo
// hacía que el INSERT fallara con "el valor es demasiado largo para varying(20)").
func uniqueSuffix() string {
	return uuid.NewString()[:8]
}

func (f *testFixture) createCharacter(t *testing.T, username string) string {
	t.Helper()
	accountID := uuid.NewString()
	_, err := f.db.Exec(
		`INSERT INTO accounts (id, username, email, password_hash) VALUES ($1, $2, $3, 'x')`,
		accountID, username, username+"@test.local",
	)
	if err != nil {
		t.Fatalf("creando cuenta de test: %v", err)
	}
	f.accountIDs = append(f.accountIDs, accountID)

	characterID := uuid.NewString()
	_, err = f.db.Exec(
		`INSERT INTO characters (id, account_id, rom_id, nickname, map_id) VALUES ($1, $2, 'emerald_es', $3, 'test_map')`,
		characterID, accountID, username,
	)
	if err != nil {
		t.Fatalf("creando personaje de test: %v", err)
	}
	return characterID
}

// cleanup borra todo lo creado por la fixture. party_groups.leader_char_id y
// guilds.leader_char_id NO tienen ON DELETE CASCADE (a diferencia de los *_members, que sí
// cascadean por char_id) — si un test deja un grupo/gremio sin disolver, borrar la cuenta
// violaría la FK. Se borran explícitamente acá, antes de las cuentas, sin asumir que cada test
// dejó todo disuelto por su cuenta.
func (f *testFixture) cleanup(t *testing.T) {
	t.Helper()
	f.db.Exec(`DELETE FROM party_groups WHERE leader_char_id IN (SELECT id FROM characters WHERE account_id = ANY($1::uuid[]))`, pqArray(f.accountIDs))
	f.db.Exec(`DELETE FROM guilds WHERE leader_char_id IN (SELECT id FROM characters WHERE account_id = ANY($1::uuid[]))`, pqArray(f.accountIDs))
	for _, aid := range f.accountIDs {
		if _, err := f.db.Exec(`DELETE FROM accounts WHERE id = $1`, aid); err != nil {
			t.Errorf("limpiando cuenta de test %s: %v", aid, err)
		}
	}
}

// pqArray arma el literal de array de Postgres a mano ({a,b,c}) para no traer el driver
// pq.Array solo para este helper de test — accountIDs son siempre UUIDs bien formados
// (uuid.NewString(), nunca entrada externa), así que no hay riesgo de inyección acá.
func pqArray(ids []string) string {
	out := "{"
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id
	}
	return out + "}"
}
