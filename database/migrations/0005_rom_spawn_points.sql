-- Fase RomLoader-4: el servidor necesita SABER dónde nace un personaje nuevo, pero eso no
-- debería ser una constante hardcodeada en Go ("Villa Raíz" para toda ROM, sin importar cuál) —
-- es un dato específico de cada ROM, igual de opaco para el servidor que species_id o rom_id.
-- Esta tabla reemplaza las constantes startMap/startX/startY de internal/auth/auth.go: agregar
-- soporte de spawn para una ROM nueva es un INSERT acá, no tocar código Go.
-- Sin FK a una tabla "roms": no existe tal catálogo todavía (rom_id es un VARCHAR libre en
-- characters, igual que acá) — el día que exista, se puede agregar la referencia.
CREATE TABLE rom_spawn_points (
    rom_id      VARCHAR(50) PRIMARY KEY,
    map_id      VARCHAR(50) NOT NULL,
    pos_x       INT NOT NULL,
    pos_y       INT NOT NULL
);

INSERT INTO rom_spawn_points (rom_id, map_id, pos_x, pos_y)
VALUES ('emerald_es', 'littleroot_town', 10, 12);

-- El default de characters.map_id (0001_init_schema.sql) implicaba que TODO personaje nuevo,
-- de cualquier ROM, nacía en Villa Raíz salvo que el INSERT dijera lo contrario — auth.go
-- siempre especifica el valor real así que nunca se ejercitaba en la práctica, pero es
-- información falsa a nivel de schema. Se saca para que quede claro que el spawn real sale de
-- rom_spawn_points, no de un default fijo acá.
ALTER TABLE characters ALTER COLUMN map_id DROP DEFAULT;
