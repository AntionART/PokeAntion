package wildencounter

import (
	"database/sql"
	"math/rand"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"pokemon-online/server/internal/battle"
	"pokemon-online/server/internal/inventory"
	"pokemon-online/server/internal/pokemon"
)

// Test de integración real contra Postgres (no mocks), mismo criterio que
// server/internal/battlesession/session_test.go: encuentros/captura salvajes son tan sensibles
// como una batalla PvP (persisten HP, experiencia, y crean filas nuevas en `pokemon`).

var loadCatalogsOnce sync.Once

func loadCatalogs(t *testing.T) {
	t.Helper()
	loadCatalogsOnce.Do(func() {
		if err := pokemon.LoadSpeciesCatalog("../../../data/pokemon/species.json"); err != nil {
			t.Fatalf("cargando catálogo de especies: %v", err)
		}
		if err := battle.LoadMoveCatalog("../../../data/pokemon/moves.json"); err != nil {
			t.Fatalf("cargando catálogo de movimientos: %v", err)
		}
		if err := LoadCatalogs("../../../data/pokemon/encounters.json", "../../../data/pokemon/learnsets.json"); err != nil {
			t.Fatalf("cargando catálogo de encuentros/learnsets: %v", err)
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
		t.Skipf("no se pudo conectar a la base de test (%s): %v —¿está levantada?", url, err)
	}
	return db
}

func createCharacterWithMon(t *testing.T, db *sql.DB, username string) (characterID string, pokemonID string, cleanup func()) {
	t.Helper()
	accountID := uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO accounts (id, username, email, password_hash) VALUES ($1, $2, $3, 'x')`,
		accountID, username, username+"@test.local",
	); err != nil {
		t.Fatalf("creando cuenta de test: %v", err)
	}

	characterID = uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO characters (id, account_id, rom_id, nickname, map_id) VALUES ($1, $2, 'emerald_es', $3, 'test_map')`,
		characterID, accountID, username,
	); err != nil {
		t.Fatalf("creando personaje de test: %v", err)
	}

	pokemonID = uuid.NewString()
	if _, err := db.Exec(
		`INSERT INTO pokemon (id, owner_char_id, species_id, level, personality, ot_id, hp_current, hp_max,
		                       stat_attack, stat_defense, stat_sp_attack, stat_sp_defense, stat_speed,
		                       nature, moves, location, team_slot)
		 VALUES ($1,$2,280,20,12345,67890,60,60,30,25,28,22,26,1,'[{"move_id":10,"pp_current":35,"pp_max":35}]','team',0)`,
		pokemonID, characterID,
	); err != nil {
		t.Fatalf("creando pokémon de test: %v", err)
	}

	return characterID, pokemonID, func() {
		db.Exec(`DELETE FROM accounts WHERE id = $1`, accountID)
	}
}

// TestTryEncounter_StatisticalRate confirma que la fórmula real de encuentro (encounterRate*16
// contra 2880) da una tasa observada razonablemente cercana a la esperada para Route 101
// (encounter_rate=20 -> ~11.1% por tirada) — no exacta (es una tirada de azar), pero dentro de
// un margen amplio en miles de tiradas.
func TestTryEncounter_StatisticalRate(t *testing.T) {
	loadCatalogs(t)
	rng := rand.New(rand.NewSource(1))
	hits := 0
	const trials = 20000
	for i := 0; i < trials; i++ {
		if _, _, ok := TryEncounter("MAP_ROUTE101", EncounterLand, rng); ok {
			hits++
		}
	}
	rate := float64(hits) / float64(trials)
	if rate < 0.08 || rate > 0.15 {
		t.Fatalf("tasa de encuentro observada fuera de rango: %.4f (esperaba ~0.111, margen 0.08-0.15)", rate)
	}
}

// TestTryEncounter_UnknownMap confirma que un mapa sin tabla de encuentros (ej. un pueblo)
// nunca genera un encuentro — no debe fallar ni panickear con el tipo de mapa equivocado.
func TestTryEncounter_UnknownMap(t *testing.T) {
	loadCatalogs(t)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 1000; i++ {
		if _, _, ok := TryEncounter("MAP_LITTLEROOT_TOWN", EncounterLand, rng); ok {
			t.Fatal("Littleroot Town no debería tener encuentros salvajes (es un pueblo, sin pasto alto)")
		}
	}
}

// TestAttemptCatch_MasterBallAlwaysCatches y TestAttemptCatch_WeakenedWildIsEasier confirman el
// comportamiento observable de la fórmula real de captura sin fijar un número exacto (tiene
// azar real, ver AttemptCatch).
func TestAttemptCatch_MasterBallAlwaysCatches(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 100; i++ {
		if !AttemptCatch(255, 280, 60, 60, rng) { // HP completo, catch_rate bajo, igual debe atrapar
			t.Fatal("Master Ball debería atrapar siempre, sin importar el HP/catch_rate")
		}
	}
}

func TestAttemptCatch_WeakenedWildIsEasier(t *testing.T) {
	loadCatalogs(t)
	rng := rand.New(rand.NewSource(1))
	fullHPCatches, lowHPCatches := 0, 0
	const trials = 2000
	for i := 0; i < trials; i++ {
		if AttemptCatch(1, 290, 20, 20, rng) { // Wurmple con HP completo
			fullHPCatches++
		}
		if AttemptCatch(1, 290, 1, 20, rng) { // Wurmple casi debilitado
			lowHPCatches++
		}
	}
	if lowHPCatches <= fullHPCatches {
		t.Fatalf("un salvaje casi debilitado debería atraparse más seguido: HP completo=%d, HP bajo=%d (de %d)", fullHPCatches, lowHPCatches, trials)
	}
}

// TestFullEncounter_CatchAndPersist corre un encuentro real (contra Postgres) hasta atraparlo,
// y confirma que la fila nueva en `pokemon` y pokedex_entries quedan bien — el camino completo
// de StartEncounter -> ThrowBall -> AddCaught.
func TestFullEncounter_CatchAndPersist(t *testing.T) {
	loadCatalogs(t)
	db := testDB(t)
	defer db.Close()

	charID, monID, cleanup := createCharacterWithMon(t, db, "wildtest_"+uuid.NewString()[:8])
	defer cleanup()

	invSvc := inventory.NewService(db)
	if err := invSvc.Grant(charID, inventory.ItemMasterBall, 1); err != nil {
		t.Fatalf("dando Master Ball de test: %v", err)
	}

	svc := NewService(pokemon.NewService(db), invSvc)

	var sessionID string
	var err error
	// TryEncounter tiene ~11% de probabilidad por tirada en Route 101 — reintentar con
	// semillas distintas hasta que un encuentro real dispare (no es adivinar el resultado, es
	// esperar a que la tirada de azar real dé encuentro, como caminar unos pasos en el juego).
	for seed := int64(1); seed < 200; seed++ {
		sessionID, _, _, err = svc.StartEncounter(charID, "MAP_ROUTE101", EncounterLand, "", rand.New(rand.NewSource(seed)))
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("no se disparó ningún encuentro en 200 intentos (algo está mal en TryEncounter): %v", err)
	}

	result, err := svc.ThrowBall(sessionID, charID, inventory.ItemMasterBall)
	if err != nil {
		t.Fatalf("ThrowBall: %v", err)
	}
	if result.Reason != "caught" || result.CaughtPokemon == nil {
		t.Fatalf("esperaba que la Master Ball atrapara, resultado: %+v", result)
	}

	var owner string
	if err := db.QueryRow(`SELECT owner_char_id FROM pokemon WHERE id = $1`, result.CaughtPokemon.ID).Scan(&owner); err != nil {
		t.Fatalf("el Pokémon atrapado no quedó en la base: %v", err)
	}
	if owner != charID {
		t.Fatalf("el Pokémon atrapado quedó con dueño incorrecto: %s", owner)
	}

	var caught bool
	if err := db.QueryRow(`SELECT caught FROM pokedex_entries WHERE owner_char_id = $1 AND species_id = $2`, charID, result.CaughtPokemon.Species).Scan(&caught); err == nil && !caught {
		t.Fatal("pokedex_entries.caught debería quedar en true tras atrapar")
	}

	_ = monID
}

// TestTryEncounter_Water_StatisticalRate confirma la tasa de encuentro surfeando en
// MAP_ROUTE102 (water_encounter_rate=4 -> ~2.2% por tirada, misma fórmula que land) — datos
// reales de wild_encounters.json, no inventados (ver server/cmd/gendata).
func TestTryEncounter_Water_StatisticalRate(t *testing.T) {
	loadCatalogs(t)
	rng := rand.New(rand.NewSource(2))
	hits := 0
	const trials = 30000
	for i := 0; i < trials; i++ {
		if _, _, ok := TryEncounter("MAP_ROUTE102", EncounterWater, rng); ok {
			hits++
		}
	}
	rate := float64(hits) / float64(trials)
	if rate < 0.01 || rate > 0.04 {
		t.Fatalf("tasa de encuentro de agua observada fuera de rango: %.4f (esperaba ~0.022, margen 0.01-0.04)", rate)
	}
}

// TestTryEncounter_RockSmash_StatisticalRate confirma la tasa en MAP_ROUTE111
// (rock_smash_encounter_rate=20 -> ~11.1%, misma fórmula que land/water).
func TestTryEncounter_RockSmash_StatisticalRate(t *testing.T) {
	loadCatalogs(t)
	rng := rand.New(rand.NewSource(3))
	hits := 0
	const trials = 20000
	for i := 0; i < trials; i++ {
		if _, _, ok := TryEncounter("MAP_ROUTE111", EncounterRockSmash, rng); ok {
			hits++
		}
	}
	rate := float64(hits) / float64(trials)
	if rate < 0.08 || rate > 0.15 {
		t.Fatalf("tasa de encuentro de rock smash observada fuera de rango: %.4f (esperaba ~0.111, margen 0.08-0.15)", rate)
	}
}

// TestTryFishingEncounter_FiltersByRodTier confirma que cada caña SOLO saca especies de su
// propio grupo — MAP_ROUTE102 con old_rod da Magikarp(129)/Goldeen(118), nunca Corphish(326)
// (que en ese mapa es exclusivo de good_rod/super_rod, ver encounters.json real).
func TestTryFishingEncounter_FiltersByRodTier(t *testing.T) {
	loadCatalogs(t)
	rng := rand.New(rand.NewSource(4))
	for i := 0; i < 500; i++ {
		species, _, ok := TryFishingEncounter("MAP_ROUTE102", "old_rod", rng)
		if !ok {
			t.Fatalf("TryFishingEncounter con old_rod no dio resultado (esperaba siempre ok, sin chequeo de tasa)")
		}
		if species != 129 && species != 118 {
			t.Fatalf("old_rod en MAP_ROUTE102 dio species=%d, esperaba solo 129 (Magikarp) o 118 (Goldeen)", species)
		}
	}
}

func TestTryFishingEncounter_UnknownRodOrMapReturnsFalse(t *testing.T) {
	loadCatalogs(t)
	rng := rand.New(rand.NewSource(5))
	if _, _, ok := TryFishingEncounter("MAP_ROUTE102", "caña_inventada", rng); ok {
		t.Error("una caña que no existe debería devolver ok=false")
	}
	if _, _, ok := TryFishingEncounter("MAP_LITTLEROOT_TOWN", "old_rod", rng); ok {
		t.Error("un mapa sin fishing_mons debería devolver ok=false")
	}
}

// TestStartEncounter_Fishing_RequiresOwnedRod confirma el chequeo de posesión: sin la caña, el
// encuentro se rechaza ANTES de tirar nada (no se puede pescar con una Super Rod que nunca se
// tuvo con solo mandar el string correcto).
func TestStartEncounter_Fishing_RequiresOwnedRod(t *testing.T) {
	db := testDB(t)
	loadCatalogs(t)
	charID, monID, cleanup := createCharacterWithMon(t, db, "wc_fish_"+uuid.NewString()[:8])
	defer cleanup()
	invSvc := inventory.NewService(db)
	svc := NewService(pokemon.NewService(db), invSvc)
	rng := rand.New(rand.NewSource(6))

	if _, _, _, err := svc.StartEncounter(charID, "MAP_ROUTE102", EncounterFishing, "old_rod", rng); err != ErrMissingRod {
		t.Errorf("StartEncounter fishing sin caña = %v, esperaba ErrMissingRod", err)
	}

	if err := invSvc.Grant(charID, inventory.ItemOldRod, 1); err != nil {
		t.Fatalf("dando la caña de test: %v", err)
	}
	if _, _, _, err := svc.StartEncounter(charID, "MAP_ROUTE102", EncounterFishing, "no_existe", rng); err != ErrInvalidRodTier {
		t.Errorf("StartEncounter con rod_tier inválido = %v, esperaba ErrInvalidRodTier", err)
	}

	// Con la caña real ya en el inventario, el encuentro debe poder arrancar (fishing no tiene
	// chequeo de tasa, ver TryFishingEncounter — siempre "pica" en este test).
	_, _, _, err := svc.StartEncounter(charID, "MAP_ROUTE102", EncounterFishing, "old_rod", rng)
	if err != nil {
		t.Errorf("StartEncounter fishing con la caña ya en inventario = %v, esperaba nil", err)
	}
	_ = monID
}
