// Package inventory administra los objetos de un personaje (tabla `inventory_items`, ya
// existía en 0001_init_schema.sql pero sin ningún código de servidor que la usara) y el
// catálogo de los pocos objetos de curación reales que hoy se pueden usar en batalla (Bag).
// IDs de include/constants/items.h de pokeemerald — no inventados.
package inventory

import (
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrNotEnoughItems = errors.New("no tenés ese objeto (o no tenés suficiente cantidad)")
	ErrUnknownItem    = errors.New("objeto no reconocido")
)

// Los 7 objetos de curación + 4 Poké Balls reales soportados en batalla hoy — Bag no es un
// inventario/tienda completo todavía (ver hueco conocido en el roadmap del proyecto), solo lo
// mínimo para que "usar un objeto en batalla" y "atrapar un Pokémon salvaje" sean reales y no
// un menú vacío / una captura garantizada sin costo.
const (
	ItemPotion      = 13
	ItemFullRestore = 19
	ItemMaxPotion   = 20
	ItemHyperPotion = 21
	ItemSuperPotion = 22
	ItemRevive      = 24
	ItemMaxRevive   = 25

	ItemMasterBall = 1
	ItemUltraBall  = 2
	ItemGreatBall  = 3
	ItemPokeBall   = 4

	// Cañas de pescar (ver server/internal/wildencounter) — objetos "llave", no se consumen al
	// usarlas (a diferencia de las Poké Balls), solo hace falta TENER una para pescar con ella.
	ItemOldRod   = 262
	ItemGoodRod  = 263
	ItemSuperRod = 264
)

type Effect int

const (
	EffectHealFlat   Effect = iota // cura una cantidad fija (Potion/Super Potion/Hyper Potion)
	EffectHealFull                 // cura hasta el máximo (Max Potion/Full Restore — sin curar status: no hay status implementado todavía)
	EffectRevive                   // revive con la mitad del HP máximo, solo si está debilitado
	EffectReviveFull               // revive con el HP máximo completo, solo si está debilitado
	EffectBall                     // Poké Ball — ver BallBonus, usado por server/internal/wildencounter para la fórmula de captura
)

type ItemInfo struct {
	ID         int
	Name       string
	Pocket     string // "items" o "balls" — con qué INSERT/consulta de inventory_items se maneja
	Effect     Effect
	HealAmount int     // solo válido si Effect == EffectHealFlat
	BallBonus  float64 // solo válido si Effect == EffectBall — multiplicador real de la fórmula de captura de Gen3 (255 = Master Ball, siempre atrapa)
	// Price real de src/data/items.h (pokeemerald) — 0 significa "no se vende en tiendas",
	// igual que la Master Ball en el juego real (se consigue por historia, price=0 ahí también).
	// Antes de esto no existía ninguna forma de reponer objetos más allá del kit inicial de
	// registro (ver hueco documentado en el roadmap: "Bag permanentemente vacío" una vez gastado).
	Price int
}

// Catalog: cantidades de curación/bonus de captura/precios reales de estos objetos en Emerald
// (src/data/items.h / item_use.c / battle_script_commands.c) — no hay un archivo único fácil
// de parsear como battle_moves.h para todo el catálogo de objetos, así que se transcriben a
// mano (mismo criterio que los 5 movimientos iniciales antes de tener el generador de datos:
// un conjunto chico y conocido).
var Catalog = map[int]ItemInfo{
	ItemPotion:      {ID: ItemPotion, Name: "POTION", Pocket: "items", Effect: EffectHealFlat, HealAmount: 20, Price: 300},
	ItemSuperPotion: {ID: ItemSuperPotion, Name: "SUPER POTION", Pocket: "items", Effect: EffectHealFlat, HealAmount: 50, Price: 700},
	ItemHyperPotion: {ID: ItemHyperPotion, Name: "HYPER POTION", Pocket: "items", Effect: EffectHealFlat, HealAmount: 200, Price: 1200},
	ItemMaxPotion:   {ID: ItemMaxPotion, Name: "MAX POTION", Pocket: "items", Effect: EffectHealFull, Price: 2500},
	ItemFullRestore: {ID: ItemFullRestore, Name: "FULL RESTORE", Pocket: "items", Effect: EffectHealFull, Price: 3000},
	ItemRevive:      {ID: ItemRevive, Name: "REVIVE", Pocket: "items", Effect: EffectRevive, Price: 1500},
	ItemMaxRevive:   {ID: ItemMaxRevive, Name: "MAX REVIVE", Pocket: "items", Effect: EffectReviveFull, Price: 4000},

	ItemPokeBall:   {ID: ItemPokeBall, Name: "POKE BALL", Pocket: "balls", Effect: EffectBall, BallBonus: 1, Price: 200},
	ItemGreatBall:  {ID: ItemGreatBall, Name: "GREAT BALL", Pocket: "balls", Effect: EffectBall, BallBonus: 1.5, Price: 600},
	ItemUltraBall:  {ID: ItemUltraBall, Name: "ULTRA BALL", Pocket: "balls", Effect: EffectBall, BallBonus: 2, Price: 1200},
	ItemMasterBall: {ID: ItemMasterBall, Name: "MASTER BALL", Pocket: "balls", Effect: EffectBall, BallBonus: 255}, // Price: 0 (no se vende, igual que en el juego real)

	ItemOldRod:   {ID: ItemOldRod, Name: "OLD ROD", Pocket: "key_items"},
	ItemGoodRod:  {ID: ItemGoodRod, Name: "GOOD ROD", Pocket: "key_items"},
	ItemSuperRod: {ID: ItemSuperRod, Name: "SUPER ROD", Pocket: "key_items"},
}

// IsBall: para que el cliente sepa si un objeto del Bag manda wild_throw_ball en vez de
// battle_item/wild_action al elegirlo.
func IsBall(itemID int) bool {
	info, ok := Catalog[itemID]
	return ok && info.Effect == EffectBall
}

// PurchasableItems: catálogo de la tienda, en el mismo orden estable que Catalog los define
// (Go no garantiza orden de iteración de maps) — cualquier objeto con Price > 0 se puede
// comprar. Ordenado a mano (potions de menor a mayor, luego revive, luego balls) para que el
// panel de tienda del cliente no reciba un orden distinto en cada request.
var purchasableOrder = []int{
	ItemPotion, ItemSuperPotion, ItemHyperPotion, ItemMaxPotion, ItemFullRestore,
	ItemRevive, ItemMaxRevive,
	ItemPokeBall, ItemGreatBall, ItemUltraBall,
}

func PurchasableItems() []ItemInfo {
	out := make([]ItemInfo, 0, len(purchasableOrder))
	for _, id := range purchasableOrder {
		if info := Catalog[id]; info.Price > 0 {
			out = append(out, info)
		}
	}
	return out
}

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

type Stack struct {
	ItemID   int
	Quantity int
}

// List devuelve TODOS los objetos que characterID realmente tiene (Potions/Revive/Balls) — no
// se filtra por pocket porque hoy Catalog no tiene nada más (ni llaves ni TMs ni bayas); el
// menú Bag de una batalla ofrece esto directamente. Si se agregan pockets que no deban aparecer
// en Bag (llaves, TMs), este es el lugar para filtrarlas.
func (s *Service) List(characterID string) ([]Stack, error) {
	rows, err := s.db.Query(
		`SELECT item_id, quantity FROM inventory_items WHERE owner_char_id = $1 AND quantity > 0 ORDER BY item_id`,
		characterID,
	)
	if err != nil {
		return nil, fmt.Errorf("listando inventario: %w", err)
	}
	defer rows.Close()

	var out []Stack
	for rows.Next() {
		var st Stack
		if err := rows.Scan(&st.ItemID, &st.Quantity); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Grant agrega quantity unidades de itemID al inventario de characterID (crea la fila si no
// existía, con el pocket real del catálogo) — usado para dar un kit inicial al crear personaje
// (ver main.go handleRegister) y para nada más todavía (no hay tienda/regalo in-game).
func (s *Service) Grant(characterID string, itemID, quantity int) error {
	pocket := "items"
	if info, ok := Catalog[itemID]; ok {
		pocket = info.Pocket
	}
	_, err := s.db.Exec(
		`INSERT INTO inventory_items (owner_char_id, item_id, quantity, pocket) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (owner_char_id, item_id) DO UPDATE SET quantity = inventory_items.quantity + excluded.quantity`,
		characterID, itemID, quantity, pocket,
	)
	return err
}

// HasItem chequea posesión sin consumir nada — para objetos "llave" no fungibles como las cañas
// de pescar (ver server/internal/wildencounter): pescar con la Super Rod requiere TENER una,
// pero usarla no la gasta, a diferencia de una Poké Ball.
func (s *Service) HasItem(characterID string, itemID int) (bool, error) {
	var quantity int
	err := s.db.QueryRow(
		`SELECT quantity FROM inventory_items WHERE owner_char_id = $1 AND item_id = $2`,
		characterID, itemID,
	).Scan(&quantity)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("consultando posesión de objeto: %w", err)
	}
	return quantity > 0, nil
}

// Consume descuenta 1 unidad de itemID del inventario de characterID — falla si no tiene
// ninguna (ErrNotEnoughItems), para que usar un objeto en batalla realmente gaste inventario
// real en vez de ser un heal gratis sin límite.
func (s *Service) Consume(characterID string, itemID int) error {
	res, err := s.db.Exec(
		`UPDATE inventory_items SET quantity = quantity - 1
		 WHERE owner_char_id = $1 AND item_id = $2 AND quantity > 0`,
		characterID, itemID,
	)
	if err != nil {
		return fmt.Errorf("consumiendo objeto: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotEnoughItems
	}
	return nil
}
