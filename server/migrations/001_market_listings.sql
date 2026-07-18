-- Mercado/subasta asincrónico (Fase G, ruta tipo PokeMMO): a diferencia del trade en vivo
-- (server/internal/trade), esto no necesita que ambos jugadores estén conectados a la vez —
-- uno publica un Pokémon con un precio, cualquier otro jugador lo compra cuando quiera.
--
-- NOTA: este es el primer archivo de migración versionado del proyecto. El esquema existente
-- (accounts, characters, pokemon, trade_sessions, etc.) se aplicó manualmente vía psql durante
-- el desarrollo temprano y no está trackeado como migraciones — gap conocido, no se resuelve
-- acá retroactivamente (fuera de alcance de esta tarea), pero de ahora en más los cambios de
-- esquema quedan documentados en server/migrations/.

CREATE TABLE market_listings (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    seller_char_id UUID NOT NULL REFERENCES characters(id) ON DELETE CASCADE,
    pokemon_id     UUID NOT NULL REFERENCES pokemon(id),
    price          INTEGER NOT NULL CHECK (price > 0),
    status         VARCHAR(10) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'sold', 'cancelled')),
    buyer_char_id  UUID REFERENCES characters(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    sold_at        TIMESTAMPTZ
);

-- La mayoría de las consultas son "traeme las publicaciones activas" — índice parcial porque
-- sold/cancelled nunca se vuelven a listar, no hace falta indexarlas.
CREATE INDEX idx_market_listings_active ON market_listings(created_at DESC) WHERE status = 'active';
CREATE INDEX idx_market_listings_seller ON market_listings(seller_char_id) WHERE status = 'active';

-- pokemon.location reutiliza 'in_trade' para "bloqueado, no disponible" — un Pokémon publicado
-- en el mercado está en el mismo estado lógico que uno ofrecido en un trade en vivo (bloqueado,
-- no se puede usar en otro lado), así que no hace falta agregar un valor nuevo al CHECK
-- constraint existente. locked_for_trade_id (sin FK real, ver server/internal/trade/trade.go)
-- se reutiliza igual, apuntando al id de market_listings en vez de trade_sessions.
