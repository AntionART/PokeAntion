-- Fase RomLoader-6: agrega el spawn point de Pokémon Emerald (USA/Europe, BPEE) — validado
-- empíricamente contra la ROM real (ver memory-maps/emerald_us.json). Mismo pueblo/coordenada
-- que emerald_es porque ambas ROMs arrancan en el mismo punto lógico del juego (Villa Raíz /
-- Littleroot Town, 10,12) — es un INSERT, no un cambio de código: así se prueba agregar
-- soporte para una ROM nueva sin tocar internal/auth/auth.go.
INSERT INTO rom_spawn_points (rom_id, map_id, pos_x, pos_y)
VALUES ('emerald_us', 'littleroot_town', 10, 12)
ON CONFLICT (rom_id) DO NOTHING;
