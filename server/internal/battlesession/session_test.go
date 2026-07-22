package battlesession

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"pokemon-online/server/internal/pokemon"
)

// Test de integración real contra Postgres (no mocks), mismo criterio que
// server/internal/trade/trade_test.go: esta es la única capa que conecta el motor de combate
// puro (server/internal/battle, ya probado en aislamiento) con la base de datos real — el
// punto de mayor riesgo (species types, HP persistido, fin de sesión) no tenía ningún test
// hasta ahora.

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

// createActivePokemon inserta el Pokémon en team_slot=0 de characterID con stats/movimientos
// REALES (mismos valores que battle.TestSimulatedBattle: Torchic y Mudkip a nivel 5) — así el
// resultado de la batalla es el mismo ya validado en aislamiento, no datos inventados nuevos.
func (f *testFixture) createActivePokemon(t *testing.T, characterID string, speciesID int, hp, attack, defense, spAttack, spDefense, speed int, moveIDs [2]int) string {
	t.Helper()
	pokemonID := uuid.NewString()
	moves := []pokemon.MoveSlot{
		{MoveID: moveIDs[0], PPCurrent: 35, PPMax: 35},
		{MoveID: moveIDs[1], PPCurrent: 40, PPMax: 40},
	}
	movesJSON, err := json.Marshal(moves)
	if err != nil {
		t.Fatalf("serializando moves de test: %v", err)
	}
	_, err = f.db.Exec(
		`INSERT INTO pokemon (id, owner_char_id, species_id, level, personality, ot_id, hp_current, hp_max,
		                       stat_attack, stat_defense, stat_sp_attack, stat_sp_defense, stat_speed,
		                       nature, moves, location, team_slot)
		 VALUES ($1,$2,$3,5,12345,67890,$4,$4,$5,$6,$7,$8,$9,1,$10,'team',0)`,
		pokemonID, characterID, speciesID, hp, attack, defense, spAttack, spDefense, speed, movesJSON,
	)
	if err != nil {
		t.Fatalf("creando pokémon activo de test: %v", err)
	}
	return pokemonID
}

func (f *testFixture) cleanup(t *testing.T) {
	t.Helper()
	for _, aid := range f.accountIDs {
		if _, err := f.db.Exec(`DELETE FROM accounts WHERE id = $1`, aid); err != nil {
			t.Errorf("limpiando cuenta de test %s: %v", aid, err)
		}
	}
}

func hpOf(t *testing.T, db *sql.DB, pokemonID string) int {
	t.Helper()
	var hp int
	if err := db.QueryRow(`SELECT hp_current FROM pokemon WHERE id = $1`, pokemonID).Scan(&hp); err != nil {
		t.Fatalf("consultando hp de %s: %v", pokemonID, err)
	}
	return hp
}

// TestFullBattle_ChallengeToEnd corre el ciclo completo (Challenge -> Accept -> N turnos de
// SubmitAction -> fin) contra Postgres real, con los mismos datos que la prueba pura de
// server/internal/battle (Torchic vs Mudkip nivel 5) — confirma que la capa de persistencia
// (species types vía pokemon.SpeciesTypes, HP escrito cada turno, sesión limpiada al terminar)
// no rompe un resultado ya validado en aislamiento.
func TestFullBattle_ChallengeToEnd(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	f := newFixture(db)
	defer f.cleanup(t)

	charA := f.createCharacter(t, "battletest_a")
	charB := f.createCharacter(t, "battletest_b")
	// A = Torchic (species 280, Fuego), B = Mudkip (species 283, Agua) — mismos IDs que
	// pokemon.SpeciesTorchic/SpeciesMudkip, mismos stats que battle.TestSimulatedBattle.
	pokemonA := f.createActivePokemon(t, charA, 280, 19, 15, 9, 16, 10, 9, [2]int{10, 45})  // Scratch, Growl
	pokemonB := f.createActivePokemon(t, charB, 283, 20, 17, 12, 12, 12, 7, [2]int{33, 45}) // Tackle, Growl

	svc := NewService(pokemon.NewService(db))

	sessionID := svc.Challenge(charA, charB)
	if sessionID == "" {
		t.Fatal("Challenge no devolvió un session_id")
	}

	gotA, gotB, viewA, viewB, err := svc.Accept(sessionID)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if gotA != charA || gotB != charB {
		t.Fatalf("Accept devolvió personajes inesperados: %s / %s", gotA, gotB)
	}
	if viewA.CurrentHP != 19 || viewA.MaxHP != 19 {
		t.Fatalf("HP inicial de A inesperado: %+v", viewA)
	}
	if viewB.CurrentHP != 20 || viewB.MaxHP != 20 {
		t.Fatalf("HP inicial de B inesperado: %+v", viewB)
	}

	// Segundo Accept debe fallar: la sesión ya no está en "pending".
	if _, _, _, _, err := svc.Accept(sessionID); err != ErrInvalidState {
		t.Fatalf("esperaba ErrInvalidState en un segundo Accept, dio: %v", err)
	}

	turns := 0
	var final *TurnResult
	for turns < 50 {
		turns++
		waiting, err := svc.SubmitAction(sessionID, charA, 0)
		if err != nil {
			t.Fatalf("SubmitAction(A) turno %d: %v", turns, err)
		}
		if waiting != nil {
			t.Fatalf("SubmitAction(A) no debería resolver el turno todavía (falta B)")
		}

		result, err := svc.SubmitAction(sessionID, charB, 0)
		if err != nil {
			t.Fatalf("SubmitAction(B) turno %d: %v", turns, err)
		}
		if result == nil {
			t.Fatalf("SubmitAction(B) debería resolver el turno (ya mandaron ambos lados)")
		}
		if len(result.Events) == 0 {
			t.Fatalf("turno %d sin eventos", turns)
		}
		if result.Finished {
			final = result
			break
		}
	}

	if final == nil {
		t.Fatal("la batalla no terminó en 50 turnos")
	}
	if final.WinnerCharID == "" || final.LoserCharID == "" || final.WinnerCharID == final.LoserCharID {
		t.Fatalf("ganador/perdedor inconsistente: %+v", final)
	}

	// El HP final en la tabla `pokemon` tiene que coincidir con el que reportó el último turno,
	// para ambos lados (no solo el que perdió) — persistido en cada turno, no solo al final.
	if got := hpOf(t, db, pokemonA); got != final.HPByCharacter[charA] {
		t.Fatalf("HP persistido de A (%d) no coincide con el reportado (%d)", got, final.HPByCharacter[charA])
	}
	if got := hpOf(t, db, pokemonB); got != final.HPByCharacter[charB] {
		t.Fatalf("HP persistido de B (%d) no coincide con el reportado (%d)", got, final.HPByCharacter[charB])
	}

	// Tras terminar, la sesión ya no existe: cualquier acción posterior debe fallar limpio.
	if _, err := svc.SubmitAction(sessionID, charA, 0); err != ErrSessionNotFound {
		t.Fatalf("esperaba ErrSessionNotFound tras el fin de la batalla, dio: %v", err)
	}
}

// TestCancel_RemovesSession confirma que Cancel (usado por battle_decline y por desconexión)
// realmente borra la sesión, para no dejarla "viva" indefinidamente si nadie la acepta.
func TestCancel_RemovesSession(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	f := newFixture(db)
	defer f.cleanup(t)

	charA := f.createCharacter(t, "battletest_cancel_a")
	charB := f.createCharacter(t, "battletest_cancel_b")

	svc := NewService(pokemon.NewService(db))
	sessionID := svc.Challenge(charA, charB)

	gotA, gotB, err := svc.Cancel(sessionID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if gotA != charA || gotB != charB {
		t.Fatalf("Cancel devolvió personajes inesperados: %s / %s", gotA, gotB)
	}

	if _, _, err := svc.Cancel(sessionID); err != ErrSessionNotFound {
		t.Fatalf("esperaba ErrSessionNotFound en un segundo Cancel, dio: %v", err)
	}
}
