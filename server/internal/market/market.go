// Package market implementa el mercado/subasta asincrónico (Fase G, ruta tipo PokeMMO):
// a diferencia de internal/trade (que necesita a los dos jugadores conectados a la vez), acá
// un jugador publica un Pokémon con un precio y cualquier otro lo compra cuando quiera, sin
// coordinar horarios. Mismo patrón que trade.Service: un Service con métodos que operan
// directo sobre las tablas en transacciones cuando hace falta atomicidad.
package market

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotOwner          = errors.New("el pokémon no te pertenece")
	ErrAlreadyLocked     = errors.New("el pokémon ya está bloqueado en otra transacción")
	ErrListingNotFound   = errors.New("publicación no encontrada o ya no está activa")
	ErrNotSeller         = errors.New("solo quien publicó puede cancelar la publicación")
	ErrOwnListing        = errors.New("no podés comprar tu propia publicación")
	ErrInsufficientFunds = errors.New("no tenés suficiente dinero")
)

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// Listing es un renglón de mercado con los datos ya resueltos (nombre del vendedor, resumen
// del Pokémon) para no obligar al router a hacer queries adicionales por cada uno.
type Listing struct {
	ID             string
	SellerCharID   string
	SellerNickname string
	PokemonID      string
	SpeciesID      int
	PokemonName    string
	Level          int
	Price          int
	CreatedAt      time.Time
}

// List bloquea el Pokémon (mismo mecanismo que trade.Service.SetOffer: location='in_trade')
// y crea la publicación. El Pokémon deja de estar disponible para trade/equipo/PC hasta que
// se cancele o se venda.
func (s *Service) List(sellerCharID, pokemonID string, price int) (string, error) {
	if price <= 0 {
		return "", errors.New("el precio debe ser mayor a cero")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var owner, location string
	if err := tx.QueryRow(`SELECT owner_char_id, location FROM pokemon WHERE id = $1 FOR UPDATE`, pokemonID).
		Scan(&owner, &location); err != nil {
		return "", err
	}
	if owner != sellerCharID {
		return "", ErrNotOwner
	}
	if location == "in_trade" {
		return "", ErrAlreadyLocked
	}

	listingID := uuid.NewString()
	if _, err := tx.Exec(
		`INSERT INTO market_listings (id, seller_char_id, pokemon_id, price) VALUES ($1, $2, $3, $4)`,
		listingID, sellerCharID, pokemonID, price,
	); err != nil {
		return "", err
	}
	if _, err := tx.Exec(
		`UPDATE pokemon SET location = 'in_trade', locked_for_trade_id = $1 WHERE id = $2`,
		listingID, pokemonID,
	); err != nil {
		return "", err
	}

	return listingID, tx.Commit()
}

// Cancel libera el Pokémon bloqueado sin transferir nada. Solo el vendedor puede cancelar.
func (s *Service) Cancel(listingID, callerCharID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var sellerID, pokemonID, status string
	if err := tx.QueryRow(`SELECT seller_char_id, pokemon_id, status FROM market_listings WHERE id = $1 FOR UPDATE`, listingID).
		Scan(&sellerID, &pokemonID, &status); err != nil {
		if err == sql.ErrNoRows {
			return ErrListingNotFound
		}
		return err
	}
	if sellerID != callerCharID {
		return ErrNotSeller
	}
	if status != "active" {
		return ErrListingNotFound
	}

	if _, err := tx.Exec(`UPDATE market_listings SET status = 'cancelled' WHERE id = $1`, listingID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE pokemon SET location = 'pc', locked_for_trade_id = NULL WHERE id = $1`, pokemonID); err != nil {
		return err
	}

	return tx.Commit()
}

// PurchaseResult identifica a las dos partes de una compra ya completada, para que el router
// pueda notificar a ambas (comprador: qué recibió; vendedor: que se vendió y por cuánto).
type PurchaseResult struct {
	SellerCharID string
	PokemonID    string
	Price        int
}

// Buy es la operación atómica completa: descuenta dinero al comprador, se lo acredita al
// vendedor, transfiere el Pokémon, y cierra la publicación — todo o nada, igual que
// trade.Service.Confirm. FOR UPDATE en characters.money evita que dos compras simultáneas de
// jugadores distintos pisen el saldo del mismo vendedor.
func (s *Service) Buy(listingID, buyerCharID string) (*PurchaseResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var sellerID, pokemonID, status string
	var price int
	if err := tx.QueryRow(`SELECT seller_char_id, pokemon_id, price, status FROM market_listings WHERE id = $1 FOR UPDATE`, listingID).
		Scan(&sellerID, &pokemonID, &price, &status); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrListingNotFound
		}
		return nil, err
	}
	if status != "active" {
		return nil, ErrListingNotFound
	}
	if sellerID == buyerCharID {
		return nil, ErrOwnListing
	}

	var buyerMoney int
	if err := tx.QueryRow(`SELECT money FROM characters WHERE id = $1 FOR UPDATE`, buyerCharID).Scan(&buyerMoney); err != nil {
		return nil, err
	}
	if buyerMoney < price {
		return nil, ErrInsufficientFunds
	}

	if _, err := tx.Exec(`UPDATE characters SET money = money - $1 WHERE id = $2`, price, buyerCharID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE characters SET money = money + $1 WHERE id = $2`, price, sellerID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		`UPDATE pokemon SET owner_char_id = $1, location = 'pc', pc_box_id = NULL, pc_box_slot = NULL, locked_for_trade_id = NULL WHERE id = $2`,
		buyerCharID, pokemonID,
	); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		`UPDATE market_listings SET status = 'sold', buyer_char_id = $1, sold_at = now() WHERE id = $2`,
		buyerCharID, listingID,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &PurchaseResult{SellerCharID: sellerID, PokemonID: pokemonID, Price: price}, nil
}

const listingSelectColumns = `
	ml.id, ml.seller_char_id, c.nickname, p.id, p.species_id, COALESCE(p.nickname, ''), p.level, ml.price, ml.created_at`

func scanListings(rows *sql.Rows) ([]Listing, error) {
	defer rows.Close()
	var out []Listing
	for rows.Next() {
		var l Listing
		if err := rows.Scan(&l.ID, &l.SellerCharID, &l.SellerNickname, &l.PokemonID, &l.SpeciesID, &l.PokemonName, &l.Level, &l.Price, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListActive devuelve las publicaciones activas de TODOS los vendedores (para "explorar
// mercado"), más recientes primero, hasta `limit`.
func (s *Service) ListActive(limit int) ([]Listing, error) {
	rows, err := s.db.Query(`
		SELECT`+listingSelectColumns+`
		FROM market_listings ml
		JOIN pokemon p ON p.id = ml.pokemon_id
		JOIN characters c ON c.id = ml.seller_char_id
		WHERE ml.status = 'active'
		ORDER BY ml.created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return scanListings(rows)
}

// MyListings devuelve las publicaciones activas de UN vendedor (para "mis publicaciones", con
// opción de cancelar).
func (s *Service) MyListings(sellerCharID string) ([]Listing, error) {
	rows, err := s.db.Query(`
		SELECT`+listingSelectColumns+`
		FROM market_listings ml
		JOIN pokemon p ON p.id = ml.pokemon_id
		JOIN characters c ON c.id = ml.seller_char_id
		WHERE ml.status = 'active' AND ml.seller_char_id = $1
		ORDER BY ml.created_at DESC`, sellerCharID)
	if err != nil {
		return nil, err
	}
	return scanListings(rows)
}
