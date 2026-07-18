// Package character maneja atributos de personalización del personaje que no encajan en
// ningún otro servicio existente (no es social, ni trade, ni mercado) — hoy solo el color de
// sprite, pensado para poder sumar más (nombre visible, título, etc.) sin reabrir router.go.
package character

import "database/sql"

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
