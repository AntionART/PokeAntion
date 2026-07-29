package pokemon

import (
	"database/sql"
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// TestExpForLevel_MatchesRealGen3Values verifica las 6 fórmulas reales de Gen3 (ver
// src/data/pokemon/experience_tables.h del checkout de pokeemerald) contra valores conocidos
// y ya verificados independientemente (no adivinados — ver gba_memory_scanning_method en
// memoria: nunca confiar en un número de memoria sin validar) para nivel 100 de cada curva:
// Medium Fast=1,000,000, Erratic=600,000, Fluctuating=1,640,000, Medium Slow=1,059,860,
// Fast=800,000, Slow=1,250,000 — estos son los totales bien documentados de cada curva.
func TestExpForLevel_MatchesRealGen3Values(t *testing.T) {
	cases := []struct {
		name       string
		growthRate int
		level      int
		want       int
	}{
		{"MediumFast_lvl100", 0, 100, 1000000},
		{"Erratic_lvl100", 1, 100, 600000},
		{"Fluctuating_lvl100", 2, 100, 1640000},
		{"MediumSlow_lvl100", 3, 100, 1059860},
		{"Fast_lvl100", 4, 100, 800000},
		{"Slow_lvl100", 5, 100, 1250000},
		{"MediumFast_lvl50", 0, 50, 125000},
		{"Erratic_lvl50", 1, 50, 125000},
		{"Fluctuating_lvl50", 2, 50, 142500},
		{"MediumSlow_lvl50", 3, 50, 117360},
		{"Fast_lvl50", 4, 50, 100000},
		{"Slow_lvl50", 5, 50, 156250},
		{"AnyGrowthRate_lvl1_is1", 3, 1, 1},
		{"AnyGrowthRate_lvl0_is0", 5, 0, 0},
		{"UnknownGrowthRate_fallsBackToMediumFast", 99, 100, 1000000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := expForLevel(c.growthRate, c.level)
			if got != c.want {
				t.Errorf("expForLevel(%d, %d) = %d, esperaba %d", c.growthRate, c.level, got, c.want)
			}
		})
	}
}

var loadCatalogsOnce sync.Once

func loadTestCatalogs(t *testing.T) {
	t.Helper()
	loadCatalogsOnce.Do(func() {
		if err := LoadSpeciesCatalog("../../../data/pokemon/species.json"); err != nil {
			t.Fatalf("cargando catálogo de especies: %v", err)
		}
		if err := LoadLearnsetCatalog("../../../data/pokemon/learnsets.json"); err != nil {
			t.Fatalf("cargando catálogo de learnsets: %v", err)
		}
	})
}

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

// createTestPokemon crea una cuenta+personaje+Pokémon reales para probar AddExperience/
// LearnMove contra Postgres de verdad — mismo criterio que trade_test.go. Torchic (species 280)
// es GROWTH_MEDIUM_SLOW (growth_rate=3, ver species.json), no Medium Fast — a propósito, para
// que el test de verdad ejercite una curva DISTINTA a la universal que se usaba antes.
func createTestPokemon(t *testing.T, db *sql.DB, level, experience int, moves string) (pokemonID, characterID string, cleanup func()) {
	t.Helper()
	accountID := uuid.NewString()
	username := "pk_" + uuid.NewString()[:8]
	if _, err := db.Exec(
		`INSERT INTO accounts (id, username, email, password_hash) VALUES ($1, $2, $3, 'x')`,
		accountID, username, username+"@test.local",
	); err != nil {
		t.Fatalf("creando cuenta: %v", err)
	}
	characterID = uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO characters (id, account_id, rom_id, nickname, map_id) VALUES ($1, $2, 'emerald_es', $3, 'test_map')`,
		characterID, accountID, username,
	); err != nil {
		t.Fatalf("creando personaje: %v", err)
	}

	pokemonID = uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO pokemon (id, owner_char_id, species_id, level, experience, hp_current, hp_max,
		 stat_attack, stat_defense, stat_sp_attack, stat_sp_defense, stat_speed, nature, location, team_slot, moves)
		 VALUES ($1, $2, 280, $3, $4, 20, 20, 10, 10, 10, 10, 10, 1, 'team', 0, $5)`,
		pokemonID, characterID, level, experience, moves,
	); err != nil {
		t.Fatalf("creando pokémon: %v", err)
	}

	return pokemonID, characterID, func() { db.Exec(`DELETE FROM accounts WHERE id = $1`, accountID) }
}

func TestAddExperience_UsesSpeciesGrowthCurve(t *testing.T) {
	db := testDB(t)
	loadTestCatalogs(t)
	svc := NewService(db)

	// Torchic a nivel 5 (Medium Slow): expForLevel(3,5)=475, expForLevel(3,6)=724 (verificado
	// con la misma fórmula real ya probada en TestExpForLevel_MatchesRealGen3Values).
	startExp := expForLevel(3, 5)
	neededForLevel6 := expForLevel(3, 6) - startExp

	pokemonID, _, cleanup := createTestPokemon(t, db, 5, startExp, "[]")
	defer cleanup()

	// Un experience gain de justo UNO menos de lo necesario NO debe subir de nivel.
	leveledUp, newLevel, _, err := svc.AddExperience(pokemonID, neededForLevel6-1)
	if err != nil {
		t.Fatalf("AddExperience (insuficiente): %v", err)
	}
	if leveledUp || newLevel != 5 {
		t.Errorf("con 1 de exp menos de lo necesario: leveledUp=%v newLevel=%d, esperaba false/5", leveledUp, newLevel)
	}

	// El experience restante (justo 1) debe alcanzar para completar el nivel 6.
	leveledUp2, newLevel2, _, err := svc.AddExperience(pokemonID, 1)
	if err != nil {
		t.Fatalf("AddExperience (completar): %v", err)
	}
	if !leveledUp2 || newLevel2 != 6 {
		t.Errorf("tras completar el exp exacto: leveledUp=%v newLevel=%d, esperaba true/6", leveledUp2, newLevel2)
	}
}

func TestAddExperience_ReturnsRealNewlyLearnedMoves(t *testing.T) {
	db := testDB(t)
	loadTestCatalogs(t)
	svc := NewService(db)

	// Torchic aprende el movimiento 116 en nivel 7 (dato real de learnsets.json, verificado
	// antes de escribir este test, no adivinado). Arrancar en nivel 5 con experience justo en
	// el umbral, y dar de sobra para llegar a nivel 9 sin pasarse — debe cruzar el nivel 7 y
	// devolver el 116 entre los aprendidos.
	startExp := expForLevel(3, 5)
	pokemonID, _, cleanup := createTestPokemon(t, db, 5, startExp, "[]")
	defer cleanup()

	gain := expForLevel(3, 9) - startExp
	leveledUp, newLevel, learnedMoveIDs, err := svc.AddExperience(pokemonID, gain)
	if err != nil {
		t.Fatalf("AddExperience: %v", err)
	}
	if !leveledUp || newLevel != 9 {
		t.Fatalf("leveledUp=%v newLevel=%d, esperaba true/9", leveledUp, newLevel)
	}
	found := false
	for _, id := range learnedMoveIDs {
		if id == 116 {
			found = true
		}
	}
	if !found {
		t.Errorf("learnedMoveIDs = %v, esperaba que incluyera el movimiento 116 (real de Torchic nivel 7)", learnedMoveIDs)
	}
}

func TestLearnMove_CapsAtFourMoves(t *testing.T) {
	db := testDB(t)
	loadTestCatalogs(t)
	svc := NewService(db)

	pokemonID, _, cleanup := createTestPokemon(t, db, 10, expForLevel(3, 10), "[]")
	defer cleanup()

	for i, moveID := range []int{1, 2, 3, 4} {
		ok, err := svc.LearnMove(pokemonID, moveID, 20)
		if err != nil {
			t.Fatalf("LearnMove #%d: %v", i, err)
		}
		if !ok {
			t.Fatalf("LearnMove #%d (moveID=%d) = false, esperaba true (todavía hay lugar)", i, moveID)
		}
	}

	// El quinto movimiento no debe entrar: ya tiene 4.
	ok, err := svc.LearnMove(pokemonID, 5, 20)
	if err != nil {
		t.Fatalf("LearnMove #5: %v", err)
	}
	if ok {
		t.Errorf("LearnMove con 4 movimientos ya aprendidos devolvió true, esperaba false (sin lugar)")
	}

	var movesRaw []byte
	if err := db.QueryRow(`SELECT moves FROM pokemon WHERE id = $1`, pokemonID).Scan(&movesRaw); err != nil {
		t.Fatalf("consultando moves: %v", err)
	}
	var moves []MoveSlot
	if err := json.Unmarshal(movesRaw, &moves); err != nil {
		t.Fatalf("parseando moves: %v", err)
	}
	if len(moves) != 4 {
		t.Errorf("moves final = %+v, esperaba exactamente 4 (el quinto no debía entrar)", moves)
	}
}

func TestReplaceMove_OverwritesSlotForRealOwner(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)

	pokemonID, characterID, cleanup := createTestPokemon(t, db, 10, expForLevel(3, 10), `[{"move_id":1,"pp_current":35,"pp_max":35},{"move_id":2,"pp_current":25,"pp_max":25},{"move_id":3,"pp_current":10,"pp_max":10},{"move_id":4,"pp_current":15,"pp_max":15}]`)
	defer cleanup()

	if err := svc.ReplaceMove(characterID, pokemonID, 2, 99, 20); err != nil {
		t.Fatalf("ReplaceMove: %v", err)
	}

	moves, err := svc.MovesOf(pokemonID)
	if err != nil {
		t.Fatalf("MovesOf: %v", err)
	}
	if len(moves) != 4 || moves[2].MoveID != 99 || moves[2].PPCurrent != 20 || moves[2].PPMax != 20 {
		t.Errorf("moves tras ReplaceMove = %+v, esperaba slot 2 reemplazado por moveID=99 pp=20/20", moves)
	}
	// Los otros 3 slots no deben haberse tocado.
	if moves[0].MoveID != 1 || moves[1].MoveID != 2 || moves[3].MoveID != 4 {
		t.Errorf("ReplaceMove tocó slots que no debía: %+v", moves)
	}
}

func TestReplaceMove_RejectsNonOwner(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)

	pokemonID, _, cleanup := createTestPokemon(t, db, 10, expForLevel(3, 10), `[{"move_id":1,"pp_current":35,"pp_max":35},{"move_id":2,"pp_current":25,"pp_max":25},{"move_id":3,"pp_current":10,"pp_max":10},{"move_id":4,"pp_current":15,"pp_max":15}]`)
	defer cleanup()

	// Un characterID de otro dueño (uno que ni siquiera existe) no debe poder tocar el moveset.
	if err := svc.ReplaceMove("00000000-0000-0000-0000-000000000000", pokemonID, 0, 99, 20); err != ErrNotOwner {
		t.Errorf("ReplaceMove de un no-dueño = %v, esperaba ErrNotOwner", err)
	}

	moves, err := svc.MovesOf(pokemonID)
	if err != nil {
		t.Fatalf("MovesOf: %v", err)
	}
	if moves[0].MoveID != 1 {
		t.Errorf("ReplaceMove de un no-dueño modificó el moveset igual: %+v", moves)
	}
}

func TestReplaceMove_RejectsInvalidSlot(t *testing.T) {
	db := testDB(t)
	svc := NewService(db)

	pokemonID, characterID, cleanup := createTestPokemon(t, db, 10, expForLevel(3, 10), `[{"move_id":1,"pp_current":35,"pp_max":35}]`)
	defer cleanup()

	if err := svc.ReplaceMove(characterID, pokemonID, 3, 99, 20); err != ErrInvalidMoveSlot {
		t.Errorf("ReplaceMove con slot fuera de rango = %v, esperaba ErrInvalidMoveSlot", err)
	}
}
