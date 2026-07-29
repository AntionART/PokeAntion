package battlesession

import (
	"database/sql"
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"pokemon-online/server/internal/battle"
	"pokemon-online/server/internal/inventory"
	"pokemon-online/server/internal/pokemon"
)

// loadCatalogsOnce asegura que pokemon.SpeciesTypes/battle.MoveByID tengan datos reales antes
// de cualquier test — sin esto, SpeciesTypes(280) devolvería (0,0) y ResolveTurn no encontraría
// ningún movimiento (MoveByID siempre ok=false), igual que en battle_test.go.
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
	})
}

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
	return f.createTeamPokemon(t, characterID, 0, speciesID, hp, attack, defense, spAttack, spDefense, speed, moveIDs)
}

// createTeamPokemon es createActivePokemon con team_slot elegible — para probar equipos de más
// de un Pokémon (cambio de Pokémon a mitad de combate, ver TestSwitch_ForcedAfterFaint).
func (f *testFixture) createTeamPokemon(t *testing.T, characterID string, teamSlot int, speciesID int, hp, attack, defense, spAttack, spDefense, speed int, moveIDs [2]int) string {
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
		 VALUES ($1,$2,$3,5,12345,67890,$4,$4,$5,$6,$7,$8,$9,1,$10,'team',$11)`,
		pokemonID, characterID, speciesID, hp, attack, defense, spAttack, spDefense, speed, movesJSON, teamSlot,
	)
	if err != nil {
		t.Fatalf("creando pokémon de equipo de test: %v", err)
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

func setHP(t *testing.T, db *sql.DB, pokemonID string, hp int) {
	t.Helper()
	if _, err := db.Exec(`UPDATE pokemon SET hp_current = $1 WHERE id = $2`, hp, pokemonID); err != nil {
		t.Fatalf("bajando hp de test: %v", err)
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
	loadCatalogs(t)
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

	svc := NewService(pokemon.NewService(db), inventory.NewService(db))

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
		waiting, err := svc.SubmitAction(sessionID, charA, ActionRequest{MoveSlot: 0})
		if err != nil {
			t.Fatalf("SubmitAction(A) turno %d: %v", turns, err)
		}
		if waiting != nil {
			t.Fatalf("SubmitAction(A) no debería resolver el turno todavía (falta B)")
		}

		result, err := svc.SubmitAction(sessionID, charB, ActionRequest{MoveSlot: 0})
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
	if _, err := svc.SubmitAction(sessionID, charA, ActionRequest{MoveSlot: 0}); err != ErrSessionNotFound {
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

	svc := NewService(pokemon.NewService(db), inventory.NewService(db))
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

// TestSwitch_ForcedAfterFaint prueba el camino nuevo de equipos de más de un Pokémon: B tiene
// un primer Pokémon con 1 HP (cae seguro con cualquier golpe de A) y un segundo de respaldo.
// Confirma la regla real de Gen3: cambiar de Pokémon reemplaza el turno entero (no se ataca Y
// se cambia a la vez), y que el ataque de A ese mismo intercambio golpea al Pokémon YA
// cambiado, no al que se debilitó.
func TestSwitch_ForcedAfterFaint(t *testing.T) {
	loadCatalogs(t)
	db := testDB(t)
	defer db.Close()

	f := newFixture(db)
	defer f.cleanup(t)

	charA := f.createCharacter(t, "battletest_switch_a")
	charB := f.createCharacter(t, "battletest_switch_b")
	pokemonA := f.createActivePokemon(t, charA, 280, 19, 15, 9, 16, 10, 9, [2]int{10, 45}) // Torchic
	f.createTeamPokemon(t, charB, 0, 283, 1, 17, 12, 12, 12, 7, [2]int{33, 45})            // Mudkip débil: 1 HP
	pokemonB2 := f.createTeamPokemon(t, charB, 1, 283, 20, 17, 12, 12, 12, 7, [2]int{33, 45})

	svc := NewService(pokemon.NewService(db), inventory.NewService(db))
	sessionID := svc.Challenge(charA, charB)
	if _, _, _, _, err := svc.Accept(sessionID); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// Turno 1: A ataca, B ataca — el Mudkip de 1 HP de B debería caer sin importar el orden
	// (Torchic pega al menos 1 de daño siempre que acierte, y Scratch tiene 100% de precisión).
	if _, err := svc.SubmitAction(sessionID, charA, ActionRequest{MoveSlot: 0}); err != nil {
		t.Fatalf("SubmitAction(A) turno 1: %v", err)
	}
	result, err := svc.SubmitAction(sessionID, charB, ActionRequest{MoveSlot: 0})
	if err != nil {
		t.Fatalf("SubmitAction(B) turno 1: %v", err)
	}
	if result == nil {
		t.Fatal("turno 1 no se resolvió")
	}
	if result.NeedsSwitch != charB {
		t.Fatalf("esperaba que B necesite cambiar de Pokémon, NeedsSwitch=%q", result.NeedsSwitch)
	}
	if result.Finished {
		t.Fatal("la batalla no debería terminar: a B le queda un Pokémon vivo")
	}

	// Mientras B necesita cambiar, un battle_action normal de B debe rechazarse.
	if _, err := svc.SubmitAction(sessionID, charB, ActionRequest{MoveSlot: 0}); err != ErrMustSwitch {
		t.Fatalf("esperaba ErrMustSwitch, dio: %v", err)
	}

	// A ya manda su jugada del intercambio siguiente (un ataque normal) antes de que B cambie —
	// tiene que quedar pendiente hasta que B efectivamente cambie.
	if _, err := svc.SubmitAction(sessionID, charA, ActionRequest{MoveSlot: 0}); err != nil {
		t.Fatalf("SubmitAction(A) turno 2: %v", err)
	}

	result, err = svc.SubmitAction(sessionID, charB, ActionRequest{IsSwitch: true, TeamSlot: 1})
	if err != nil {
		t.Fatalf("SubmitAction(B) switch: %v", err)
	}
	if result == nil {
		t.Fatal("el cambio de B debería resolver el intercambio (A ya tenía su jugada pendiente)")
	}

	sawSwitch := false
	for _, e := range result.Events {
		if e.Type == battle.EventSwitch && e.ActorCharID == charB {
			sawSwitch = true
			if e.TargetSpecies != 283 {
				t.Fatalf("evento de cambio con especie inesperada: %d", e.TargetSpecies)
			}
		}
	}
	if !sawSwitch {
		t.Fatalf("no se emitió un evento de cambio: %+v", result.Events)
	}
	// El segundo Mudkip (20 HP) recibió el ataque pendiente de A este mismo intercambio — su HP
	// tiene que haber bajado, no seguir en el máximo.
	if hp := hpOf(t, db, pokemonB2); hp >= 20 {
		t.Fatalf("el Pokémon recién cambiado debería haber recibido el ataque pendiente de A, hp=%d", hp)
	}

	_ = pokemonA
}

// TestItem_HealAndConsumesInventory prueba el Bag: A está dañado, usa una Potion (cura 20,
// tope al máximo) en vez de atacar — confirma que el HP sube, que el objeto se descuenta del
// inventario real (no es un heal gratis), y que intentar usar uno que ya no tiene falla limpio.
func TestItem_HealAndConsumesInventory(t *testing.T) {
	loadCatalogs(t)
	db := testDB(t)
	defer db.Close()

	f := newFixture(db)
	defer f.cleanup(t)

	charA := f.createCharacter(t, "battletest_item_a")
	charB := f.createCharacter(t, "battletest_item_b")
	pokemonA := f.createActivePokemon(t, charA, 280, 19, 15, 9, 16, 10, 9, [2]int{10, 45})
	f.createActivePokemon(t, charB, 283, 20, 17, 12, 12, 12, 7, [2]int{33, 45})
	setHP(t, db, pokemonA, 5) // dañado, para que la Potion (cura 20) tope en el máximo (19), no se pase

	invSvc := inventory.NewService(db)
	if err := invSvc.Grant(charA, inventory.ItemPotion, 1); err != nil {
		t.Fatalf("dando Potion de test: %v", err)
	}

	svc := NewService(pokemon.NewService(db), invSvc)
	sessionID := svc.Challenge(charA, charB)
	if _, _, _, _, err := svc.Accept(sessionID); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	if _, err := svc.SubmitAction(sessionID, charA, ActionRequest{IsItem: true, ItemID: inventory.ItemPotion, TeamSlot: 0}); err != nil {
		t.Fatalf("SubmitAction(A) item: %v", err)
	}
	result, err := svc.SubmitAction(sessionID, charB, ActionRequest{MoveSlot: 0})
	if err != nil {
		t.Fatalf("SubmitAction(B): %v", err)
	}
	if result == nil {
		t.Fatal("el intercambio debería haberse resuelto")
	}

	sawItem := false
	for _, e := range result.Events {
		if e.Type == battle.EventItemUsed && e.ActorCharID == charA {
			sawItem = true
			if e.ItemID != inventory.ItemPotion {
				t.Fatalf("evento de objeto con item_id inesperado: %d", e.ItemID)
			}
		}
	}
	if !sawItem {
		t.Fatalf("no se emitió un evento de uso de objeto: %+v", result.Events)
	}
	// Usar un objeto no vuelve invulnerable: B igual atacó este intercambio (A solo no
	// contraatacó). 5 + 20 = 25 topado en el máximo real (19) MENOS el golpe de B. Cota real
	// (no adivinada): Tackle nv5, atk=17 vs def=9 dan damage base 5 -> sin crítico
	// floor(5*[0.85,1.0))=4 SIEMPRE; con crítico (1/16) floor(5*2*[0.85,1.0)) es 8 U 9 (9 cuando
	// randRoll>=0.9). Mínimo real posible: 19-9=10 — con "hp <= 10" el test fallaba de forma
	// intermitente (~1/24 corridas) exactamente en ese caso válido de crítico+roll alto, no por
	// un bug del motor. Cota corregida a "< 10" para no rechazar un resultado real posible.
	hp := hpOf(t, db, pokemonA)
	if hp < 10 || hp > 19 {
		t.Fatalf("HP tras la Potion inesperado: %d (esperaba >=10 y <=19: curado y tope de máximo, menos el golpe de B)", hp)
	}

	remaining, err := invSvc.List(charA)
	if err != nil {
		t.Fatalf("listando inventario: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("la Potion debería haberse consumido del todo (tenía 1), quedó: %+v", remaining)
	}

	// Sin Potions ya, un segundo intento debe rechazarse limpio (no romper la sesión).
	if _, err := svc.SubmitAction(sessionID, charA, ActionRequest{IsItem: true, ItemID: inventory.ItemPotion, TeamSlot: 0}); err != inventory.ErrNotEnoughItems {
		t.Fatalf("esperaba ErrNotEnoughItems, dio: %v", err)
	}
}
