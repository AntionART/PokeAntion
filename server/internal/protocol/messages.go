// Package protocol define el contrato de mensajes cliente<->servidor.
// Debe mantenerse en sincronía con /common/protocol/PROTOCOL.md y con el
// stub de cliente en /client-stub/Protocol. Cualquier cambio aquí es un
// cambio de contrato: avisar en ambos lados.
package protocol

import "encoding/json"

// Envelope es la envoltura común de todo mensaje, en ambas direcciones.
type Envelope struct {
	Type    string          `json:"type"`
	Seq     int64           `json:"seq,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

// ---- Autenticación ----

type LoginPayload struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// SessionToken, si viene presente, reautentica con el JWT emitido en un login_ok
	// previo y hace que Username/Password se ignoren. Pensado para reconexión del
	// WebSocket sin volver a pedir credenciales.
	SessionToken string `json:"session_token,omitempty"`
}

type LoginOKPayload struct {
	AccountID    string `json:"account_id"`
	CharacterID  string `json:"character_id"`
	SessionToken string `json:"session_token"`
	MapID        string `json:"map_id"`
	PosX         int    `json:"pos_x"`
	PosY         int    `json:"pos_y"`
	// Color es el color de sprite persistido del personaje ("default" si nunca lo cambió) —
	// el cliente lo necesita para saber qué mostrar como seleccionado en el picker y para
	// mandarlo él mismo en su primer "move" (ver world.AllowedSpriteColors).
	Color string `json:"color"`
}

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ---- Movimiento / presencia ----

type MovePayload struct {
	MapID  string `json:"map_id"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Facing string `json:"facing"`
	State  string `json:"state"` // walking | idle | battling
}

type PlayerUpdatePayload struct {
	CharacterID string `json:"character_id"`
	Nickname    string `json:"nickname"`
	SpriteID    string `json:"sprite_id"`
	MapID       string `json:"map_id"`
	X           int    `json:"x"`
	Y           int    `json:"y"`
	Facing      string `json:"facing"`
	State       string `json:"state"`
	Color       string `json:"color"`
}

// SetColorPayload cambia el color de sprite del emisor — ver world.AllowedSpriteColors para
// los valores válidos. El servidor rechaza cualquier otro valor (no es un campo de texto libre).
type SetColorPayload struct {
	Color string `json:"color"`
}

// MoveRejectedPayload se manda de vuelta SOLO al emisor cuando su "move" excede la
// velocidad físicamente posible (ver ws.Hub.UpdatePosition) — X/Y son la posición corregida
// (la última válida conocida por el servidor) para que el cliente se resincronice.
type MoveRejectedPayload struct {
	MapID  string `json:"map_id"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Facing string `json:"facing"`
}

// MapPlayersSnapshotPayload se manda UNA vez, justo tras login_ok, con quién ya está en el
// mapa de spawn del jugador. Sin esto, un jugador recién conectado no ve a nadie hasta que
// esa otra persona mande su primer "move" (player_update no se re-emite espontáneamente).
type MapPlayersSnapshotPayload struct {
	Players []PlayerUpdatePayload `json:"players"`
}

// ---- Chat ----

type SendChatPayload struct {
	Channel           string `json:"channel"` // global | local | private | command
	TargetCharacterID string `json:"target_character_id,omitempty"`
	Message           string `json:"message"`
}

type ChatMessagePayload struct {
	Channel         string `json:"channel"`
	FromCharacterID string `json:"from_character_id"`
	FromNickname    string `json:"from_nickname"`
	Message         string `json:"message"`
	Timestamp       string `json:"timestamp"`
}

// ---- Pokémon (resumen para UI, no el detalle completo de combate) ----

type PokemonSummaryPayload struct {
	ID        string `json:"id"`
	SpeciesID int    `json:"species_id"`
	Nickname  string `json:"nickname,omitempty"`
	Level     int    `json:"level"`
	Location  string `json:"location"` // team | pc | in_trade
}

type MyPokemonListPayload struct {
	Pokemon []PokemonSummaryPayload `json:"pokemon"`
}

// ---- Trade ----

type TradeRequestPayload struct {
	TargetCharacterID string `json:"target_character_id"`
}

// TradeRequestReceivedPayload se manda al destinatario de un trade_request. Incluye el
// nickname (no solo el character_id) porque el cliente todavía no tiene forma de resolver
// nicknames a partir de un ID salvo por los jugadores que ya vio moverse en su mapa.
type TradeRequestReceivedPayload struct {
	TradeSessionID  string `json:"trade_session_id"`
	FromCharacterID string `json:"from_character_id"`
	FromNickname    string `json:"from_nickname"`
}

// TradeOfferUpdatedPayload se manda a AMBOS participantes cada vez que uno fija su oferta,
// para que la UI de cada uno pueda mostrar en vivo qué ofreció el otro antes de confirmar
// (sin esto, cada jugador confirmaría "a ciegas", sin ver qué le están por dar a cambio).
type TradeOfferUpdatedPayload struct {
	TradeSessionID string                `json:"trade_session_id"`
	CharacterID    string                `json:"character_id"` // quién fijó esta oferta
	Pokemon        PokemonSummaryPayload `json:"pokemon"`
}

type TradeOfferSetPayload struct {
	TradeSessionID string `json:"trade_session_id"`
	PokemonID      string `json:"pokemon_id"`
}

type TradeConfirmPayload struct {
	TradeSessionID string `json:"trade_session_id"`
}

type TradeCompletedPayload struct {
	TradeSessionID  string `json:"trade_session_id"`
	CharAReceivedID string `json:"char_a_received_pokemon_id"`
	CharBReceivedID string `json:"char_b_received_pokemon_id"`
}

// TradeSessionRefPayload es el payload compartido por trade_accept y trade_decline:
// ambos solo necesitan identificar la sesión.
type TradeSessionRefPayload struct {
	TradeSessionID string `json:"trade_session_id"`
}

type TradeCancelledPayload struct {
	TradeSessionID string `json:"trade_session_id"`
	Reason         string `json:"reason"`
}

// ---- Amigos ----

type FriendRequestPayload struct {
	TargetUsername string `json:"target_username"`
}

type FriendRequestReceivedPayload struct {
	FromAccountID string `json:"from_account_id"`
	FromUsername  string `json:"from_username"`
}

// FriendRefPayload identifica a la otra cuenta en friend_accept/friend_decline/friend_remove.
type FriendRefPayload struct {
	TargetAccountID string `json:"target_account_id"`
}

type FriendListEntryPayload struct {
	AccountID string `json:"account_id"`
	Username  string `json:"username"`
	Online    bool   `json:"online"`
}

type FriendListPayload struct {
	Friends []FriendListEntryPayload `json:"friends"`
}

type FriendStatusUpdatePayload struct {
	AccountID string `json:"account_id"`
	Online    bool   `json:"online"`
}

// ---- Grupos (party) ----

type PartyInvitePayload struct {
	TargetCharacterID string `json:"target_character_id"`
}

type PartyInviteReceivedPayload struct {
	PartyID         string `json:"party_id"`
	FromCharacterID string `json:"from_character_id"`
	FromNickname    string `json:"from_nickname"`
}

// PartyRefPayload identifica al grupo en party_accept/party_decline/party_leave.
type PartyRefPayload struct {
	PartyID string `json:"party_id"`
}

type PartyMemberPayload struct {
	CharacterID string `json:"character_id"`
	Nickname    string `json:"nickname"`
	MapID       string `json:"map_id"`
	X           int    `json:"x"`
	Y           int    `json:"y"`
	IsLeader    bool   `json:"is_leader"`
}

type PartyUpdatePayload struct {
	PartyID string               `json:"party_id"`
	Members []PartyMemberPayload `json:"members"`
}

type PartyDisbandedPayload struct {
	PartyID string `json:"party_id"`
	Reason  string `json:"reason"`
}

// ---- Mercado (asincrónico — a diferencia de trade, no necesita a los dos jugadores online) ----

type MarketListPayload struct {
	PokemonID string `json:"pokemon_id"`
	Price     int    `json:"price"`
}

// MarketListingRefPayload identifica una publicación en market_cancel/market_buy y en las
// confirmaciones market_list_ok/market_cancelled.
type MarketListingRefPayload struct {
	ListingID string `json:"listing_id"`
}

type MarketListingPayload struct {
	ListingID      string                `json:"listing_id"`
	SellerCharID   string                `json:"seller_char_id"`
	SellerNickname string                `json:"seller_nickname"`
	Pokemon        PokemonSummaryPayload `json:"pokemon"`
	Price          int                   `json:"price"`
}

// MarketListingsPayload es la respuesta tanto a market_browse (publicaciones de todos) como a
// market_my_listings (solo las propias) — el cliente ya sabe cuál pidió, no hace falta
// distinguir el tipo de mensaje.
type MarketListingsPayload struct {
	Listings []MarketListingPayload `json:"listings"`
}

// MarketPurchasedPayload va al COMPRADOR: qué recibió y por cuánto.
type MarketPurchasedPayload struct {
	ListingID string                `json:"listing_id"`
	Pokemon   PokemonSummaryPayload `json:"pokemon"`
	Price     int                   `json:"price"`
}

// MarketSoldPayload va al VENDEDOR (si está conectado en ese momento): que se vendió, quién
// compró y por cuánto. Si el vendedor está offline simplemente no lo recibe — se entera la
// próxima vez que abra "mis publicaciones" (ya no va a estar listada).
type MarketSoldPayload struct {
	ListingID     string `json:"listing_id"`
	BuyerNickname string `json:"buyer_nickname"`
	Price         int    `json:"price"`
}

// ---- Gremios (persistentes — a diferencia de party, no se disuelven al desconectarse todos) ----

type GuildCreatePayload struct {
	Name string `json:"name"`
}

type GuildInvitePayload struct {
	TargetCharacterID string `json:"target_character_id"`
}

type GuildInviteReceivedPayload struct {
	GuildID         string `json:"guild_id"`
	GuildName       string `json:"guild_name"`
	FromCharacterID string `json:"from_character_id"`
	FromNickname    string `json:"from_nickname"`
}

// GuildRefPayload identifica al gremio en guild_accept/guild_decline.
type GuildRefPayload struct {
	GuildID string `json:"guild_id"`
}

type GuildKickPayload struct {
	TargetCharacterID string `json:"target_character_id"`
}

type GuildMemberPayload struct {
	CharacterID string `json:"character_id"`
	Nickname    string `json:"nickname"`
	Online      bool   `json:"online"`
	IsOfficer   bool   `json:"is_officer"`
}

type GuildUpdatePayload struct {
	GuildID string                `json:"guild_id"`
	Name    string                `json:"name"`
	Members []GuildMemberPayload `json:"members"`
}

type GuildDisbandedPayload struct {
	GuildID string `json:"guild_id"`
	Reason  string `json:"reason"`
}

// Helper para construir un Envelope de salida sin repetir json.Marshal en cada handler.
func NewEnvelope(msgType string, payload any) (Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{Type: msgType, Payload: raw}, nil
}
