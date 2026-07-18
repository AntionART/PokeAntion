-- =========================================================
-- Pokémon Online — Esquema inicial de base de datos
-- Motor: PostgreSQL 15+
-- =========================================================

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ---------------------------------------------------------
-- Cuentas
-- ---------------------------------------------------------
CREATE TABLE accounts (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    username        VARCHAR(20) UNIQUE NOT NULL,
    email           VARCHAR(255) UNIQUE NOT NULL,
    password_hash   TEXT NOT NULL,              -- bcrypt/argon2id, nunca texto plano
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at   TIMESTAMPTZ,
    is_banned       BOOLEAN NOT NULL DEFAULT false,
    ban_reason      TEXT
);

-- ---------------------------------------------------------
-- Personajes (un personaje por cuenta por ahora; deja espacio a futuro
-- para múltiples slots/ROMs por cuenta)
-- ---------------------------------------------------------
CREATE TABLE characters (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    account_id      UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    rom_id          VARCHAR(50) NOT NULL,        -- ej: 'emerald_us', 'firered_us'
    nickname        VARCHAR(20) NOT NULL,
    sprite_id       VARCHAR(50) NOT NULL DEFAULT 'default',
    map_id          VARCHAR(50) NOT NULL DEFAULT 'littleroot_town',
    pos_x           INTEGER NOT NULL DEFAULT 0,
    pos_y           INTEGER NOT NULL DEFAULT 0,
    facing          VARCHAR(10) NOT NULL DEFAULT 'down', -- up/down/left/right
    money           INTEGER NOT NULL DEFAULT 3000,
    playtime_secs   INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(account_id, rom_id)
);

CREATE INDEX idx_characters_map ON characters(map_id);

-- ---------------------------------------------------------
-- Pokémon (instancias individuales, sea en equipo, PC o en trade)
-- ---------------------------------------------------------
CREATE TABLE pokemon (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    owner_char_id   UUID NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    species_id      INTEGER NOT NULL,            -- id de especie según la ROM (ej. 1=Bulbasaur)
    nickname        VARCHAR(20),
    level           SMALLINT NOT NULL CHECK (level BETWEEN 1 AND 100),
    experience      INTEGER NOT NULL DEFAULT 0,
    hp_current      SMALLINT NOT NULL,
    hp_max          SMALLINT NOT NULL,
    stat_attack     SMALLINT NOT NULL,
    stat_defense    SMALLINT NOT NULL,
    stat_sp_attack  SMALLINT NOT NULL,
    stat_sp_defense SMALLINT NOT NULL,
    stat_speed      SMALLINT NOT NULL,
    nature          SMALLINT NOT NULL,
    ability_slot    SMALLINT NOT NULL DEFAULT 0,
    is_shiny        BOOLEAN NOT NULL DEFAULT false,
    held_item_id    INTEGER,
    moves           JSONB NOT NULL DEFAULT '[]', -- [{move_id, pp_current, pp_max}, ...]
    ivs             JSONB NOT NULL DEFAULT '{}', -- {hp,atk,def,spa,spd,spe}
    evs             JSONB NOT NULL DEFAULT '{}',
    original_trainer_id UUID REFERENCES characters(id),
    -- Ubicación lógica del Pokémon: 'team' (equipo activo, slot 0-5), 'pc' (caja), 'in_trade'
    location        VARCHAR(10) NOT NULL DEFAULT 'pc' CHECK (location IN ('team','pc','in_trade')),
    team_slot       SMALLINT CHECK (team_slot BETWEEN 0 AND 5),
    pc_box_id       UUID,
    pc_box_slot     SMALLINT,
    caught_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_for_trade_id UUID,  -- referencia a trade_sessions.id mientras está bloqueado
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_pokemon_owner ON pokemon(owner_char_id);
CREATE UNIQUE INDEX idx_pokemon_team_slot ON pokemon(owner_char_id, team_slot) WHERE location = 'team';

-- ---------------------------------------------------------
-- Cajas del PC
-- ---------------------------------------------------------
CREATE TABLE pc_boxes (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    owner_char_id   UUID NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    box_number      SMALLINT NOT NULL,
    box_name        VARCHAR(30) NOT NULL DEFAULT 'Box',
    UNIQUE(owner_char_id, box_number)
);

ALTER TABLE pokemon
    ADD CONSTRAINT fk_pokemon_pc_box FOREIGN KEY (pc_box_id) REFERENCES pc_boxes(id);

-- ---------------------------------------------------------
-- Inventario / objetos
-- ---------------------------------------------------------
CREATE TABLE inventory_items (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    owner_char_id   UUID NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    item_id         INTEGER NOT NULL,
    quantity        INTEGER NOT NULL CHECK (quantity >= 0),
    pocket          VARCHAR(20) NOT NULL DEFAULT 'items', -- items/key_items/tms/berries/balls
    UNIQUE(owner_char_id, item_id)
);

-- ---------------------------------------------------------
-- Pokédex
-- ---------------------------------------------------------
CREATE TABLE pokedex_entries (
    owner_char_id   UUID NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    species_id      INTEGER NOT NULL,
    seen            BOOLEAN NOT NULL DEFAULT false,
    caught          BOOLEAN NOT NULL DEFAULT false,
    first_seen_at   TIMESTAMPTZ,
    first_caught_at TIMESTAMPTZ,
    PRIMARY KEY (owner_char_id, species_id)
);

-- ---------------------------------------------------------
-- Misiones / progreso de historia
-- ---------------------------------------------------------
CREATE TABLE quest_progress (
    owner_char_id   UUID NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    quest_id        VARCHAR(50) NOT NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'not_started', -- not_started/in_progress/completed
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_char_id, quest_id)
);

-- ---------------------------------------------------------
-- Amigos
-- ---------------------------------------------------------
CREATE TABLE friendships (
    account_id      UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    friend_id       UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    status          VARCHAR(10) NOT NULL DEFAULT 'pending', -- pending/accepted/blocked
    requested_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at     TIMESTAMPTZ,
    PRIMARY KEY (account_id, friend_id),
    CHECK (account_id <> friend_id)
);

-- ---------------------------------------------------------
-- Grupos (parties)
-- ---------------------------------------------------------
CREATE TABLE party_groups (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    leader_char_id  UUID NOT NULL REFERENCES characters(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE party_members (
    party_id        UUID NOT NULL REFERENCES party_groups(id) ON DELETE CASCADE,
    char_id         UUID NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    joined_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (party_id, char_id)
);

-- ---------------------------------------------------------
-- Intercambios (trading) — sesión + log de auditoría
-- ---------------------------------------------------------
CREATE TABLE trade_sessions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    char_a_id       UUID NOT NULL REFERENCES characters(id),
    char_b_id       UUID NOT NULL REFERENCES characters(id),
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',
        -- pending -> accepted -> offering -> confirmed_a -> confirmed_b -> completed
        -- puede pasar a: cancelled, failed_rollback
    offer_a_pokemon_id UUID REFERENCES pokemon(id),
    offer_b_pokemon_id UUID REFERENCES pokemon(id),
    confirmed_a     BOOLEAN NOT NULL DEFAULT false,
    confirmed_b     BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ,
    cancelled_reason TEXT
);

CREATE TABLE trade_log (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    trade_session_id UUID NOT NULL REFERENCES trade_sessions(id),
    event           VARCHAR(30) NOT NULL, -- created/accepted/offer_set/confirmed/completed/cancelled/rollback
    detail          JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------
-- Configuración del jugador (opciones de cliente, guardadas en servidor)
-- ---------------------------------------------------------
CREATE TABLE player_settings (
    owner_char_id   UUID PRIMARY KEY REFERENCES characters(id) ON DELETE CASCADE,
    settings        JSONB NOT NULL DEFAULT '{}' -- volumen, idioma, atajos de chat, etc.
);

-- ---------------------------------------------------------
-- Trigger genérico para updated_at
-- ---------------------------------------------------------
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_characters_updated_at BEFORE UPDATE ON characters
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_pokemon_updated_at BEFORE UPDATE ON pokemon
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
