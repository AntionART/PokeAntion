// Espejo en C# de server/internal/protocol/messages.go
// Debe mantenerse en sincronía manualmente con ese archivo y con /common/protocol/PROTOCOL.md
// hasta que se automatice la generación (ej. vía un generador desde un schema compartido).
//
// Pensado para usarse dentro de un proyecto Godot 4 (C#) como capa de Network/Protocol.

using System;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace PokemonOnline.Protocol
{
    public class Envelope
    {
        [JsonPropertyName("type")]
        public string Type { get; set; } = "";

        [JsonPropertyName("seq")]
        public long Seq { get; set; }

        [JsonPropertyName("payload")]
        public JsonElement Payload { get; set; }
    }

    public class ErrorPayload
    {
        [JsonPropertyName("code")] public string Code { get; set; } = "";
        [JsonPropertyName("message")] public string Message { get; set; } = "";
    }

    public class LoginPayload
    {
        [JsonPropertyName("username")] public string Username { get; set; } = "";
        [JsonPropertyName("password")] public string Password { get; set; } = "";
        // Si se completa, reautentica con un JWT de una sesión anterior (login_ok.SessionToken)
        // en vez de usuario/contraseña. Útil para reconectar el WebSocket sin volver a pedirlas.
        [JsonPropertyName("session_token")] public string? SessionToken { get; set; }
    }

    public class LoginOKPayload
    {
        [JsonPropertyName("account_id")] public string AccountId { get; set; } = "";
        [JsonPropertyName("character_id")] public string CharacterId { get; set; } = "";
        [JsonPropertyName("session_token")] public string SessionToken { get; set; } = "";
        [JsonPropertyName("map_id")] public string MapId { get; set; } = "";
        [JsonPropertyName("pos_x")] public int PosX { get; set; }
        [JsonPropertyName("pos_y")] public int PosY { get; set; }
        [JsonPropertyName("color")] public string Color { get; set; } = "default";
    }

    public class MovePayload
    {
        [JsonPropertyName("map_id")] public string MapId { get; set; } = "";
        [JsonPropertyName("x")] public int X { get; set; }
        [JsonPropertyName("y")] public int Y { get; set; }
        [JsonPropertyName("facing")] public string Facing { get; set; } = "down";
        [JsonPropertyName("state")] public string State { get; set; } = "idle";
    }

    public class PlayerUpdatePayload
    {
        [JsonPropertyName("character_id")] public string CharacterId { get; set; } = "";
        [JsonPropertyName("nickname")] public string Nickname { get; set; } = "";
        [JsonPropertyName("sprite_id")] public string SpriteId { get; set; } = "";
        [JsonPropertyName("map_id")] public string MapId { get; set; } = "";
        [JsonPropertyName("x")] public int X { get; set; }
        [JsonPropertyName("y")] public int Y { get; set; }
        [JsonPropertyName("facing")] public string Facing { get; set; } = "";
        [JsonPropertyName("state")] public string State { get; set; } = "";
        [JsonPropertyName("color")] public string Color { get; set; } = "default";
    }

    public class SetColorPayload
    {
        [JsonPropertyName("color")] public string Color { get; set; } = "default";
    }

    // Solo al emisor, cuando su "move" excede la velocidad físicamente posible: X/Y son la
    // última posición válida conocida por el servidor, para resincronizarse.
    public class MoveRejectedPayload
    {
        [JsonPropertyName("map_id")] public string MapId { get; set; } = "";
        [JsonPropertyName("x")] public int X { get; set; }
        [JsonPropertyName("y")] public int Y { get; set; }
        [JsonPropertyName("facing")] public string Facing { get; set; } = "";
    }

    // Una sola vez, justo tras login_ok: quién ya está en el mapa de spawn.
    public class MapPlayersSnapshotPayload
    {
        [JsonPropertyName("players")] public PlayerUpdatePayload[] Players { get; set; } = Array.Empty<PlayerUpdatePayload>();
    }

    public class SendChatPayload
    {
        [JsonPropertyName("channel")] public string Channel { get; set; } = "local";
        [JsonPropertyName("target_character_id")] public string? TargetCharacterId { get; set; }
        [JsonPropertyName("message")] public string Message { get; set; } = "";
    }

    public class ChatMessagePayload
    {
        [JsonPropertyName("channel")] public string Channel { get; set; } = "";
        [JsonPropertyName("from_character_id")] public string FromCharacterId { get; set; } = "";
        [JsonPropertyName("from_nickname")] public string FromNickname { get; set; } = "";
        [JsonPropertyName("message")] public string Message { get; set; } = "";
        [JsonPropertyName("timestamp")] public string Timestamp { get; set; } = "";
    }

    // ---- Pokémon (resumen para UI) ----

    public class PokemonSummaryPayload
    {
        [JsonPropertyName("id")] public string Id { get; set; } = "";
        [JsonPropertyName("species_id")] public int SpeciesId { get; set; }
        [JsonPropertyName("nickname")] public string? Nickname { get; set; }
        [JsonPropertyName("level")] public int Level { get; set; }
        [JsonPropertyName("location")] public string Location { get; set; } = "";
    }

    public class MyPokemonListPayload
    {
        [JsonPropertyName("pokemon")] public PokemonSummaryPayload[] Pokemon { get; set; } = Array.Empty<PokemonSummaryPayload>();
    }

    // ---- Trade ----

    public class TradeRequestPayload
    {
        [JsonPropertyName("target_character_id")] public string TargetCharacterId { get; set; } = "";
    }

    public class TradeRequestReceivedPayload
    {
        [JsonPropertyName("trade_session_id")] public string TradeSessionId { get; set; } = "";
        [JsonPropertyName("from_character_id")] public string FromCharacterId { get; set; } = "";
        [JsonPropertyName("from_nickname")] public string FromNickname { get; set; } = "";
    }

    public class TradeOfferUpdatedPayload
    {
        [JsonPropertyName("trade_session_id")] public string TradeSessionId { get; set; } = "";
        [JsonPropertyName("character_id")] public string CharacterId { get; set; } = "";
        [JsonPropertyName("pokemon")] public PokemonSummaryPayload Pokemon { get; set; } = new();
    }

    public class TradeSessionRefPayload
    {
        [JsonPropertyName("trade_session_id")] public string TradeSessionId { get; set; } = "";
    }

    public class TradeOfferSetPayload
    {
        [JsonPropertyName("trade_session_id")] public string TradeSessionId { get; set; } = "";
        [JsonPropertyName("pokemon_id")] public string PokemonId { get; set; } = "";
    }

    public class TradeCompletedPayload
    {
        [JsonPropertyName("trade_session_id")] public string TradeSessionId { get; set; } = "";
        [JsonPropertyName("char_a_received_pokemon_id")] public string CharAReceivedId { get; set; } = "";
        [JsonPropertyName("char_b_received_pokemon_id")] public string CharBReceivedId { get; set; } = "";
    }

    public class TradeCancelledPayload
    {
        [JsonPropertyName("trade_session_id")] public string TradeSessionId { get; set; } = "";
        [JsonPropertyName("reason")] public string Reason { get; set; } = "";
    }

    // ---- Amigos ----

    public class FriendRequestPayload
    {
        [JsonPropertyName("target_username")] public string TargetUsername { get; set; } = "";
    }

    public class FriendRequestReceivedPayload
    {
        [JsonPropertyName("from_account_id")] public string FromAccountId { get; set; } = "";
        [JsonPropertyName("from_username")] public string FromUsername { get; set; } = "";
    }

    public class FriendRefPayload
    {
        [JsonPropertyName("target_account_id")] public string TargetAccountId { get; set; } = "";
    }

    public class FriendListEntryPayload
    {
        [JsonPropertyName("account_id")] public string AccountId { get; set; } = "";
        [JsonPropertyName("username")] public string Username { get; set; } = "";
        [JsonPropertyName("online")] public bool Online { get; set; }
    }

    public class FriendListPayload
    {
        [JsonPropertyName("friends")] public FriendListEntryPayload[] Friends { get; set; } = Array.Empty<FriendListEntryPayload>();
    }

    public class FriendStatusUpdatePayload
    {
        [JsonPropertyName("account_id")] public string AccountId { get; set; } = "";
        [JsonPropertyName("online")] public bool Online { get; set; }
    }

    // ---- Grupos (party) ----

    public class PartyInvitePayload
    {
        [JsonPropertyName("target_character_id")] public string TargetCharacterId { get; set; } = "";
    }

    public class PartyInviteReceivedPayload
    {
        [JsonPropertyName("party_id")] public string PartyId { get; set; } = "";
        [JsonPropertyName("from_character_id")] public string FromCharacterId { get; set; } = "";
        [JsonPropertyName("from_nickname")] public string FromNickname { get; set; } = "";
    }

    public class PartyRefPayload
    {
        [JsonPropertyName("party_id")] public string PartyId { get; set; } = "";
    }

    public class PartyMemberPayload
    {
        [JsonPropertyName("character_id")] public string CharacterId { get; set; } = "";
        [JsonPropertyName("nickname")] public string Nickname { get; set; } = "";
        [JsonPropertyName("map_id")] public string MapId { get; set; } = "";
        [JsonPropertyName("x")] public int X { get; set; }
        [JsonPropertyName("y")] public int Y { get; set; }
        [JsonPropertyName("is_leader")] public bool IsLeader { get; set; }
    }

    public class PartyUpdatePayload
    {
        [JsonPropertyName("party_id")] public string PartyId { get; set; } = "";
        [JsonPropertyName("members")] public PartyMemberPayload[] Members { get; set; } = Array.Empty<PartyMemberPayload>();
    }

    public class PartyDisbandedPayload
    {
        [JsonPropertyName("party_id")] public string PartyId { get; set; } = "";
        [JsonPropertyName("reason")] public string Reason { get; set; } = "";
    }

    // ---- Mercado (asincrónico) ----

    public class MarketListPayload
    {
        [JsonPropertyName("pokemon_id")] public string PokemonId { get; set; } = "";
        [JsonPropertyName("price")] public int Price { get; set; }
    }

    public class MarketListingRefPayload
    {
        [JsonPropertyName("listing_id")] public string ListingId { get; set; } = "";
    }

    public class MarketListingPayload
    {
        [JsonPropertyName("listing_id")] public string ListingId { get; set; } = "";
        [JsonPropertyName("seller_char_id")] public string SellerCharId { get; set; } = "";
        [JsonPropertyName("seller_nickname")] public string SellerNickname { get; set; } = "";
        [JsonPropertyName("pokemon")] public PokemonSummaryPayload Pokemon { get; set; } = new();
        [JsonPropertyName("price")] public int Price { get; set; }
    }

    public class MarketListingsPayload
    {
        [JsonPropertyName("listings")] public MarketListingPayload[] Listings { get; set; } = Array.Empty<MarketListingPayload>();
    }

    public class MarketPurchasedPayload
    {
        [JsonPropertyName("listing_id")] public string ListingId { get; set; } = "";
        [JsonPropertyName("pokemon")] public PokemonSummaryPayload Pokemon { get; set; } = new();
        [JsonPropertyName("price")] public int Price { get; set; }
    }

    public class MarketSoldPayload
    {
        [JsonPropertyName("listing_id")] public string ListingId { get; set; } = "";
        [JsonPropertyName("buyer_nickname")] public string BuyerNickname { get; set; } = "";
        [JsonPropertyName("price")] public int Price { get; set; }
    }

    // ---- Gremios (persistentes, a diferencia de party) ----

    public class GuildCreatePayload
    {
        [JsonPropertyName("name")] public string Name { get; set; } = "";
    }

    public class GuildInvitePayload
    {
        [JsonPropertyName("target_character_id")] public string TargetCharacterId { get; set; } = "";
    }

    public class GuildInviteReceivedPayload
    {
        [JsonPropertyName("guild_id")] public string GuildId { get; set; } = "";
        [JsonPropertyName("guild_name")] public string GuildName { get; set; } = "";
        [JsonPropertyName("from_character_id")] public string FromCharacterId { get; set; } = "";
        [JsonPropertyName("from_nickname")] public string FromNickname { get; set; } = "";
    }

    public class GuildRefPayload
    {
        [JsonPropertyName("guild_id")] public string GuildId { get; set; } = "";
    }

    public class GuildKickPayload
    {
        [JsonPropertyName("target_character_id")] public string TargetCharacterId { get; set; } = "";
    }

    public class GuildMemberPayload
    {
        [JsonPropertyName("character_id")] public string CharacterId { get; set; } = "";
        [JsonPropertyName("nickname")] public string Nickname { get; set; } = "";
        [JsonPropertyName("online")] public bool Online { get; set; }
        [JsonPropertyName("is_officer")] public bool IsOfficer { get; set; }
    }

    public class GuildUpdatePayload
    {
        [JsonPropertyName("guild_id")] public string GuildId { get; set; } = "";
        [JsonPropertyName("name")] public string Name { get; set; } = "";
        [JsonPropertyName("members")] public GuildMemberPayload[] Members { get; set; } = Array.Empty<GuildMemberPayload>();
    }

    public class GuildDisbandedPayload
    {
        [JsonPropertyName("guild_id")] public string GuildId { get; set; } = "";
        [JsonPropertyName("reason")] public string Reason { get; set; } = "";
    }

    // Helper de (de)serialización, para no repetir JsonSerializer.Serialize/Deserialize
    // en cada punto del cliente que arma o lee un Envelope.
    public static class ProtocolCodec
    {
        public static string Encode(string type, object payload)
        {
            var payloadJson = JsonSerializer.Serialize(payload);
            var envelope = new
            {
                type,
                payload = JsonDocument.Parse(payloadJson).RootElement
            };
            return JsonSerializer.Serialize(envelope);
        }

        public static T DecodePayload<T>(Envelope envelope)
        {
            return envelope.Payload.Deserialize<T>()
                ?? throw new InvalidOperationException("payload nulo o con formato inesperado");
        }
    }
}
