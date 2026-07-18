package trade

import (
	"database/sql"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// Tests de integración reales contra Postgres (no mocks): el módulo de trading es el más
// sensible del proyecto, así que estos tests existen específicamente para probar los
// escenarios que preocupaban desde el diseño original — duplicación por doble confirmación
// simultánea, y que cancelar/rechazar deje todo en un estado consistente.
//
// Requiere una base de Postgres real con el esquema aplicado. Por default apunta a
// pokemon_online_test (ver database/migrations/0001_init_schema.sql); se puede apuntar a
// otra con TEST_DATABASE_URL. Si no hay conexión disponible, los tests se saltan (no fallan)
// para no romper `go build`/`go vet` en un entorno sin Postgres.

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

// testFixture crea una cuenta+personaje real (vía SQL directo, sin pasar por bcrypt/auth
// para que los tests corran rápido) y un Pokémon de ese personaje. Devuelve una función de
// cleanup que borra todo lo creado, en el orden correcto para no chocar con foreign keys.
type testFixture struct {
	db         *sql.DB
	accountIDs []string
	sessionIDs []string
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
		`INSERT INTO characters (id, account_id, rom_id, nickname) VALUES ($1, $2, 'emerald_es', $3)`,
		characterID, accountID, username,
	)
	if err != nil {
		t.Fatalf("creando personaje de test: %v", err)
	}
	return characterID
}

func (f *testFixture) createPokemon(t *testing.T, ownerCharID string) string {
	t.Helper()
	pokemonID := uuid.NewString()
	_, err := f.db.Exec(
		`INSERT INTO pokemon (id, owner_char_id, species_id, level, hp_current, hp_max,
		 stat_attack, stat_defense, stat_sp_attack, stat_sp_defense, stat_speed, nature, location, team_slot)
		 VALUES ($1, $2, 25, 5, 20, 20, 10, 10, 10, 10, 10, 1, 'team', 0)`,
		pokemonID, ownerCharID,
	)
	if err != nil {
		t.Fatalf("creando pokémon de test: %v", err)
	}
	return pokemonID
}

func (f *testFixture) cleanup(t *testing.T) {
	t.Helper()
	for _, sid := range f.sessionIDs {
		f.db.Exec(`DELETE FROM trade_log WHERE trade_session_id = $1`, sid)
		f.db.Exec(`DELETE FROM trade_sessions WHERE id = $1`, sid)
	}
	for _, aid := range f.accountIDs {
		// CASCADE se encarga de characters y pokemon.
		if _, err := f.db.Exec(`DELETE FROM accounts WHERE id = $1`, aid); err != nil {
			t.Errorf("limpiando cuenta de test %s: %v", aid, err)
		}
	}
}

func pokemonOwnerAndLocation(t *testing.T, db *sql.DB, pokemonID string) (owner, location string) {
	t.Helper()
	err := db.QueryRow(`SELECT owner_char_id, location FROM pokemon WHERE id = $1`, pokemonID).Scan(&owner, &location)
	if err != nil {
		t.Fatalf("consultando pokémon %s: %v", pokemonID, err)
	}
	return owner, location
}

// TestConfirm_SimultaneousConfirmations es el escenario que más preocupaba desde el diseño
// original: ambos jugadores confirman "al mismo tiempo" (dos goroutines concurrentes). Sin
// el FOR UPDATE de Confirm(), esto podría ejecutar el intercambio dos veces y duplicar un
// Pokémon. Se corre muchas veces (no una) porque una condición de carrera puede no
// manifestarse en una sola corrida.
func TestConfirm_SimultaneousConfirmations(t *testing.T) {
	db := testDB(t)
	defer db.Close()

	for iter := 0; iter < 20; iter++ {
		f := newFixture(db)
		svc := NewService(db)

		charA := f.createCharacter(t, "raceA_"+uuid.NewString()[:8])
		charB := f.createCharacter(t, "raceB_"+uuid.NewString()[:8])
		monA := f.createPokemon(t, charA)
		monB := f.createPokemon(t, charB)

		sessionID, err := svc.RequestTrade(charA, charB)
		if err != nil {
			t.Fatalf("[iter %d] RequestTrade: %v", iter, err)
		}
		f.sessionIDs = append(f.sessionIDs, sessionID)

		if err := svc.AcceptTrade(sessionID); err != nil {
			t.Fatalf("[iter %d] AcceptTrade: %v", iter, err)
		}
		if err := svc.SetOffer(sessionID, charA, monA); err != nil {
			t.Fatalf("[iter %d] SetOffer A: %v", iter, err)
		}
		if err := svc.SetOffer(sessionID, charB, monB); err != nil {
			t.Fatalf("[iter %d] SetOffer B: %v", iter, err)
		}

		var wg sync.WaitGroup
		results := make([]*TradeResult, 2)
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			results[0], errs[0] = svc.Confirm(sessionID, charA)
		}()
		go func() {
			defer wg.Done()
			results[1], errs[1] = svc.Confirm(sessionID, charB)
		}()
		wg.Wait()

		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("[iter %d] Confirm no debería fallar nunca en este escenario: errA=%v errB=%v", iter, errs[0], errs[1])
		}

		// Exactamente UNA de las dos confirmaciones debe haber ejecutado el intercambio
		// (la que "llegó segunda" a la base de datos); la otra debe devolver nil (solo
		// registró su propia confirmación, sin completar el trade todavía en su momento).
		completions := 0
		if results[0] != nil {
			completions++
		}
		if results[1] != nil {
			completions++
		}
		if completions != 1 {
			t.Fatalf("[iter %d] se esperaba exactamente 1 resultado de trade completado entre las dos goroutines, hubo %d", iter, completions)
		}

		ownerA, locA := pokemonOwnerAndLocation(t, db, monA)
		ownerB, locB := pokemonOwnerAndLocation(t, db, monB)
		if ownerA != charB {
			t.Errorf("[iter %d] el pokémon de A debería pertenecer ahora a B: owner=%s", iter, ownerA)
		}
		if ownerB != charA {
			t.Errorf("[iter %d] el pokémon de B debería pertenecer ahora a A: owner=%s", iter, ownerB)
		}
		if locA != "pc" || locB != "pc" {
			t.Errorf("[iter %d] ambos pokémon deberían quedar en 'pc' tras el trade: locA=%s locB=%s", iter, locA, locB)
		}

		var totalRows int
		if err := db.QueryRow(`SELECT count(*) FROM pokemon WHERE id IN ($1, $2)`, monA, monB).Scan(&totalRows); err != nil {
			t.Fatalf("[iter %d] verificando anti-duplicación: %v", iter, err)
		}
		if totalRows != 2 {
			t.Fatalf("[iter %d] ANTI-DUPLICACIÓN VIOLADA: se esperaban exactamente 2 filas de pokémon, hay %d", iter, totalRows)
		}

		f.cleanup(t)
	}
}

// TestCancel_ReleasesOfferedPokemon: cancelar una sesión con una oferta ya puesta debe
// liberar el Pokémon (location vuelve a 'pc', locked_for_trade_id se limpia) para que no
// quede atrapado indefinidamente.
func TestCancel_ReleasesOfferedPokemon(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	charA := f.createCharacter(t, "cancelA_"+uuid.NewString()[:8])
	charB := f.createCharacter(t, "cancelB_"+uuid.NewString()[:8])
	monA := f.createPokemon(t, charA)

	sessionID, err := svc.RequestTrade(charA, charB)
	if err != nil {
		t.Fatalf("RequestTrade: %v", err)
	}
	f.sessionIDs = append(f.sessionIDs, sessionID)

	if err := svc.AcceptTrade(sessionID); err != nil {
		t.Fatalf("AcceptTrade: %v", err)
	}
	if err := svc.SetOffer(sessionID, charA, monA); err != nil {
		t.Fatalf("SetOffer: %v", err)
	}

	_, location := pokemonOwnerAndLocation(t, db, monA)
	if location != "in_trade" {
		t.Fatalf("el pokémon debería quedar bloqueado (in_trade) tras la oferta, location=%s", location)
	}

	if _, _, err := svc.Cancel(sessionID, "test"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	owner, location := pokemonOwnerAndLocation(t, db, monA)
	if owner != charA {
		t.Errorf("el dueño no debería cambiar al cancelar: owner=%s", owner)
	}
	if location != "pc" {
		t.Errorf("el pokémon debería liberarse a 'pc' al cancelar, quedó en: %s", location)
	}

	var lockedFor sql.NullString
	if err := db.QueryRow(`SELECT locked_for_trade_id FROM pokemon WHERE id = $1`, monA).Scan(&lockedFor); err != nil {
		t.Fatalf("consultando locked_for_trade_id: %v", err)
	}
	if lockedFor.Valid {
		t.Errorf("locked_for_trade_id debería quedar NULL tras cancelar, quedó: %s", lockedFor.String)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM trade_sessions WHERE id = $1`, sessionID).Scan(&status); err != nil {
		t.Fatalf("consultando status de la sesión: %v", err)
	}
	if status != "cancelled" {
		t.Errorf("la sesión debería quedar 'cancelled', quedó: %s", status)
	}
}

// TestSetOffer_RejectsPokemonNotOwnedByCaller: no se puede ofrecer un Pokémon que no es
// propio, incluso conociendo su ID.
func TestSetOffer_RejectsPokemonNotOwnedByCaller(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	charA := f.createCharacter(t, "ownerA_"+uuid.NewString()[:8])
	charB := f.createCharacter(t, "ownerB_"+uuid.NewString()[:8])
	monA := f.createPokemon(t, charA) // pertenece a charA

	sessionID, err := svc.RequestTrade(charA, charB)
	if err != nil {
		t.Fatalf("RequestTrade: %v", err)
	}
	f.sessionIDs = append(f.sessionIDs, sessionID)

	if err := svc.AcceptTrade(sessionID); err != nil {
		t.Fatalf("AcceptTrade: %v", err)
	}

	// charB intenta ofrecer el pokémon de charA.
	err = svc.SetOffer(sessionID, charB, monA)
	if err != ErrNotOwner {
		t.Fatalf("se esperaba ErrNotOwner, se obtuvo: %v", err)
	}

	// El pokémon no debe haber quedado bloqueado por el intento fallido.
	_, location := pokemonOwnerAndLocation(t, db, monA)
	if location == "in_trade" {
		t.Errorf("un SetOffer rechazado por ErrNotOwner no debería bloquear el pokémon, quedó: %s", location)
	}
}

// TestConfirm_RequiresBothOffers: no se puede confirmar si el otro jugador todavía no puso
// su oferta — evita completar un trade "a medias".
func TestConfirm_RequiresBothOffers(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	charA := f.createCharacter(t, "halfA_"+uuid.NewString()[:8])
	charB := f.createCharacter(t, "halfB_"+uuid.NewString()[:8])
	monA := f.createPokemon(t, charA)

	sessionID, err := svc.RequestTrade(charA, charB)
	if err != nil {
		t.Fatalf("RequestTrade: %v", err)
	}
	f.sessionIDs = append(f.sessionIDs, sessionID)

	if err := svc.AcceptTrade(sessionID); err != nil {
		t.Fatalf("AcceptTrade: %v", err)
	}
	if err := svc.SetOffer(sessionID, charA, monA); err != nil {
		t.Fatalf("SetOffer: %v", err)
	}

	// B nunca ofreció nada; A intenta confirmar.
	if _, err := svc.Confirm(sessionID, charA); err == nil {
		t.Fatalf("Confirm debería fallar si el otro jugador no ofreció nada todavía")
	}
}
