-- Sistema de batalla (Fase 2): para inyectar un Pokémon en la RAM del emulador (ver
-- RomLoader.Gen3Codec/NewGameBootstrap, cliente) hace falta personality/otId — determinan la
-- clave de cifrado y el orden de substructuras del formato real de guardado de Gen3. La tabla
-- pokemon existente (0001_init_schema.sql) no los tenía porque fue diseñada antes de necesitar
-- reproducir el formato binario exacto del juego — esto es puramente ADITIVO, no reemplaza nada
-- del diseño existente (ivs/evs/moves como JSONB siguen siendo la fuente de verdad "de juego";
-- personality/otId son solo lo que hace falta para la codificación binaria hacia el cliente).
ALTER TABLE pokemon ADD COLUMN personality BIGINT NOT NULL DEFAULT 0;
ALTER TABLE pokemon ADD COLUMN ot_id BIGINT NOT NULL DEFAULT 0;
