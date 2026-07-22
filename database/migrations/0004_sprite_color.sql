-- Color de personaje elegible por el jugador (paleta predefinida, ver internal/world/colors.go
-- para la lista permitida) — se aplica como tinte multiplicativo sobre el sprite ya decodificado
-- de OAM/VRAM, no cambia el sprite en sí. 'default' = sin tinte (colores naturales del sprite).
ALTER TABLE characters ADD COLUMN sprite_color VARCHAR(10) NOT NULL DEFAULT 'default';
