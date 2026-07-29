// Package character maneja atributos de personalización del personaje que no encajan en
// ningún otro servicio existente (no es social, ni trade, ni mercado) — hoy solo el color de
// sprite, pensado para poder sumar más (nombre visible, título, etc.) sin reabrir router.go.
package character

import (
	"database/sql"
	"errors"
)

var ErrInsufficientFunds = errors.New("no tenés suficiente dinero")

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// SetColor persiste el color de sprite elegido. El llamador es responsable de validar que
// color esté en la paleta permitida (ver world.AllowedSpriteColors) — este método no valida,
// solo guarda, para no duplicar la lista de colores en dos paquetes.
func (s *Service) SetColor(characterID, color string) error {
	_, err := s.db.Exec(`UPDATE characters SET sprite_color = $1 WHERE id = $2`, color, characterID)
	return err
}

// SetMoney persiste el dinero del personaje. Autoritativo del servidor (no del save de la
// ROM) — el cliente lo inyecta en la RAM del emulador al bootear vía
// RomLoader.NewGameBootstrap/GbaMemoryAdapter.SetMoney, nunca lo lee de ahí como fuente de
// verdad (ver gen3_save_pointers memory).
func (s *Service) SetMoney(characterID string, amount int) error {
	_, err := s.db.Exec(`UPDATE characters SET money = $1 WHERE id = $2`, amount, characterID)
	return err
}

// TryDebitMoney descuenta amount del dinero de characterID de forma atómica (UPDATE...WHERE
// money >= amount en una sola sentencia, no un SELECT seguido de un UPDATE separado) — evita
// que dos compras concurrentes del mismo personaje dejen el dinero en negativo por una
// condición de carrera. Devuelve ErrInsufficientFunds si no alcanzaba (no es un error de
// sistema, el llamador lo muestra como mensaje normal, no lo loguea como error).
func (s *Service) TryDebitMoney(characterID string, amount int) (newMoney int, err error) {
	row := s.db.QueryRow(
		`UPDATE characters SET money = money - $1 WHERE id = $2 AND money >= $1 RETURNING money`,
		amount, characterID,
	)
	if err := row.Scan(&newMoney); err != nil {
		if err == sql.ErrNoRows {
			return 0, ErrInsufficientFunds
		}
		return 0, err
	}
	return newMoney, nil
}
