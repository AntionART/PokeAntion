-- Gremios (Fase G, ruta tipo PokeMMO): a diferencia de party_groups (efímero, se disuelve al
-- quedar vacío, pensado para una sesión de juego), un gremio es persistente — sobrevive a que
-- todos sus miembros se desconecten, tiene nombre propio, y (ver internal/chat) un canal de
-- chat propio para hablarle a todo el gremio esté o no en el mismo mapa.

CREATE TABLE guilds (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name           VARCHAR(30) NOT NULL UNIQUE,
    leader_char_id UUID NOT NULL REFERENCES characters(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE guild_members (
    guild_id    UUID NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    char_id     UUID NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    is_officer  BOOLEAN NOT NULL DEFAULT FALSE,
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (char_id) -- un personaje pertenece, como mucho, a UN gremio a la vez
);

CREATE INDEX idx_guild_members_guild ON guild_members(guild_id);
