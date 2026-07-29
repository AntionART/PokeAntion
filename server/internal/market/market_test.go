package market

import (
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// Tests de integración reales contra Postgres, mismo criterio que internal/trade: el mercado
// mueve dinero real y transfiere dueño de Pokémon, así que un mock no probaría lo que
// realmente preocupa (plata que se duplica o desaparece, Pokémon que quedan en location
// inconsistente si algo falla a mitad de camino).

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

type fixture struct {
	db         *sql.DB
	accountIDs []string
}

func newFixture(db *sql.DB) *fixture { return &fixture{db: db} }

func (f *fixture) createCharacter(t *testing.T, username string, money int) string {
	t.Helper()
	accountID := uuid.NewString()
	if _, err := f.db.Exec(
		`INSERT INTO accounts (id, username, email, password_hash) VALUES ($1, $2, $3, 'x')`,
		accountID, username, username+"@test.local",
	); err != nil {
		t.Fatalf("creando cuenta: %v", err)
	}
	f.accountIDs = append(f.accountIDs, accountID)

	characterID := uuid.NewString()
	if _, err := f.db.Exec(
		`INSERT INTO characters (id, account_id, rom_id, nickname, map_id, money) VALUES ($1, $2, 'emerald_es', $3, 'test_map', $4)`,
		characterID, accountID, username, money,
	); err != nil {
		t.Fatalf("creando personaje: %v", err)
	}
	return characterID
}

func (f *fixture) createPokemon(t *testing.T, ownerCharID string) string {
	t.Helper()
	pokemonID := uuid.NewString()
	if _, err := f.db.Exec(
		`INSERT INTO pokemon (id, owner_char_id, species_id, level, hp_current, hp_max,
		 stat_attack, stat_defense, stat_sp_attack, stat_sp_defense, stat_speed, nature, location, team_slot)
		 VALUES ($1, $2, 280, 5, 20, 20, 10, 10, 10, 10, 10, 1, 'team', 0)`,
		pokemonID, ownerCharID,
	); err != nil {
		t.Fatalf("creando pokémon: %v", err)
	}
	return pokemonID
}

func (f *fixture) cleanup(t *testing.T) {
	t.Helper()
	for _, aid := range f.accountIDs {
		if _, err := f.db.Exec(`DELETE FROM accounts WHERE id = $1`, aid); err != nil {
			t.Errorf("limpiando cuenta %s: %v", aid, err)
		}
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

func pokemonOwnerAndLocation(t *testing.T, db *sql.DB, pokemonID string) (owner, location string) {
	t.Helper()
	if err := db.QueryRow(`SELECT owner_char_id, location FROM pokemon WHERE id = $1`, pokemonID).Scan(&owner, &location); err != nil {
		t.Fatalf("consultando pokémon %s: %v", pokemonID, err)
	}
	return owner, location
}

func TestMarket_ListAndBuyTransfersMoneyAndPokemon(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	seller := f.createCharacter(t, "mk_sel_"+uuid.NewString()[:6], 0)
	buyer := f.createCharacter(t, "mk_buy_"+uuid.NewString()[:6], 1000)
	mon := f.createPokemon(t, seller)

	listingID, err := svc.List(seller, mon, 500)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if owner, loc := pokemonOwnerAndLocation(t, db, mon); owner != seller || loc != "in_trade" {
		t.Errorf("tras List: owner=%q location=%q, esperaba (%q, in_trade)", owner, loc, seller)
	}

	result, err := svc.Buy(listingID, buyer)
	if err != nil {
		t.Fatalf("Buy: %v", err)
	}
	if result.SellerCharID != seller || result.PokemonID != mon || result.Price != 500 {
		t.Errorf("PurchaseResult = %+v, no coincide con lo esperado", result)
	}

	if got := moneyOf(t, db, buyer); got != 500 {
		t.Errorf("dinero del comprador tras Buy = %d, esperaba 500", got)
	}
	if got := moneyOf(t, db, seller); got != 500 {
		t.Errorf("dinero del vendedor tras Buy = %d, esperaba 500", got)
	}
	if owner, loc := pokemonOwnerAndLocation(t, db, mon); owner != buyer || loc != "pc" {
		t.Errorf("tras Buy: owner=%q location=%q, esperaba (%q, pc)", owner, loc, buyer)
	}

	// Comprar una publicación ya vendida debe fallar.
	if _, err := svc.Buy(listingID, buyer); err != ErrListingNotFound {
		t.Errorf("segundo Buy sobre la misma publicación = %v, esperaba ErrListingNotFound", err)
	}
}

func TestMarket_BuyInsufficientFunds(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	seller := f.createCharacter(t, "mk_s2_"+uuid.NewString()[:6], 0)
	buyer := f.createCharacter(t, "mk_b2_"+uuid.NewString()[:6], 10)
	mon := f.createPokemon(t, seller)

	listingID, err := svc.List(seller, mon, 500)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if _, err := svc.Buy(listingID, buyer); err != ErrInsufficientFunds {
		t.Errorf("Buy sin fondos = %v, esperaba ErrInsufficientFunds", err)
	}
	// El Pokémon debe seguir bloqueado y en manos del vendedor: la compra fallida no debe
	// tocar nada.
	if owner, loc := pokemonOwnerAndLocation(t, db, mon); owner != seller || loc != "in_trade" {
		t.Errorf("tras Buy fallido: owner=%q location=%q, esperaba (%q, in_trade) sin cambios", owner, loc, seller)
	}
	if got := moneyOf(t, db, buyer); got != 10 {
		t.Errorf("dinero del comprador tras Buy fallido = %d, esperaba 10 (sin cambios)", got)
	}
}

func TestMarket_CannotBuyOwnListing(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	seller := f.createCharacter(t, "mk_s3_"+uuid.NewString()[:6], 10000)
	mon := f.createPokemon(t, seller)

	listingID, err := svc.List(seller, mon, 500)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := svc.Buy(listingID, seller); err != ErrOwnListing {
		t.Errorf("Buy de la propia publicación = %v, esperaba ErrOwnListing", err)
	}
}

func TestMarket_CancelUnlocksPokemon(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	seller := f.createCharacter(t, "mk_s4_"+uuid.NewString()[:6], 0)
	other := f.createCharacter(t, "mk_o4_"+uuid.NewString()[:6], 10000)
	mon := f.createPokemon(t, seller)

	listingID, err := svc.List(seller, mon, 500)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Solo el vendedor puede cancelar.
	if err := svc.Cancel(listingID, other); err != ErrNotSeller {
		t.Errorf("Cancel de alguien que no es el vendedor = %v, esperaba ErrNotSeller", err)
	}

	if err := svc.Cancel(listingID, seller); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if owner, loc := pokemonOwnerAndLocation(t, db, mon); owner != seller || loc != "pc" {
		t.Errorf("tras Cancel: owner=%q location=%q, esperaba (%q, pc)", owner, loc, seller)
	}

	// Cancelar de nuevo (ya no está activa) debe fallar.
	if err := svc.Cancel(listingID, seller); err != ErrListingNotFound {
		t.Errorf("segundo Cancel = %v, esperaba ErrListingNotFound", err)
	}
	// Y comprar una publicación cancelada también.
	if _, err := svc.Buy(listingID, other); err != ErrListingNotFound {
		t.Errorf("Buy sobre publicación cancelada = %v, esperaba ErrListingNotFound", err)
	}
}

func TestMarket_ListActiveAndMyListings(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	seller := f.createCharacter(t, "mk_s5_"+uuid.NewString()[:6], 0)
	other := f.createCharacter(t, "mk_o5_"+uuid.NewString()[:6], 0)
	monA := f.createPokemon(t, seller)
	monB := f.createPokemon(t, other)

	if _, err := svc.List(seller, monA, 100); err != nil {
		t.Fatalf("List A: %v", err)
	}
	if _, err := svc.List(other, monB, 200); err != nil {
		t.Fatalf("List B: %v", err)
	}

	all, err := svc.ListActive(50)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	var foundA, foundB bool
	for _, l := range all {
		if l.PokemonID == monA {
			foundA = true
		}
		if l.PokemonID == monB {
			foundB = true
		}
	}
	if !foundA || !foundB {
		t.Errorf("ListActive no incluyó ambas publicaciones: %+v", all)
	}

	mine, err := svc.MyListings(seller)
	if err != nil {
		t.Fatalf("MyListings: %v", err)
	}
	if len(mine) != 1 || mine[0].PokemonID != monA {
		t.Errorf("MyListings(seller) = %+v, esperaba solo monA", mine)
	}
}

func TestMarket_ListSomeoneElsesPokemonRejected(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	owner := f.createCharacter(t, "mk_s6_"+uuid.NewString()[:6], 0)
	stranger := f.createCharacter(t, "mk_o6_"+uuid.NewString()[:6], 0)
	mon := f.createPokemon(t, owner)

	if _, err := svc.List(stranger, mon, 100); err != ErrNotOwner {
		t.Errorf("List de un Pokémon ajeno = %v, esperaba ErrNotOwner", err)
	}
}
