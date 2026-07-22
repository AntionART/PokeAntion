package world

import (
	"encoding/json"
	"log/slog"
	"time"

	"pokemon-online/server/internal/battlesession"
	"pokemon-online/server/internal/character"
	"pokemon-online/server/internal/chat"
	"pokemon-online/server/internal/market"
	"pokemon-online/server/internal/protocol"
	"pokemon-online/server/internal/social"
	"pokemon-online/server/internal/trade"
	"pokemon-online/server/internal/ws"
)

// Router implementa ws.Router: recibe cada mensaje ya autenticado y lo despacha
// al servicio correspondiente (movimiento, chat, trade, social...). Es la capa que
// mantiene el Hub de websockets desacoplado de la lógica de negocio.
type Router struct {
	hub       *ws.Hub
	chat      *chat.Service
	trade     *trade.Service
	friends   *social.Service
	party     *social.PartyService
	market    *market.Service
	guild     *social.GuildService
	character *character.Service
	battle    *battlesession.Service
}

func NewRouter(hub *ws.Hub, chatSvc *chat.Service, tradeSvc *trade.Service, friendsSvc *social.Service, partySvc *social.PartyService, marketSvc *market.Service, guildSvc *social.GuildService, characterSvc *character.Service, battleSvc *battlesession.Service) *Router {
	return &Router{hub: hub, chat: chatSvc, trade: tradeSvc, friends: friendsSvc, party: partySvc, market: marketSvc, guild: guildSvc, character: characterSvc, battle: battleSvc}
}

// characterIDLen es el largo fijo de un UUID en su forma con guiones ("xxxxxxxx-xxxx-...").
// Se usa como prefijo fijo (no length-prefix variable) en los paquetes de voz relayados,
// porque el payload de audio es binario arbitrario y podría contener cualquier byte —
// un separador no sería seguro, un ancho fijo sí.
const characterIDLen = 36

// HandleBinaryMessage relaya paquetes de voz (PCM16 mono crudo, ver client-engine) a los
// demás jugadores del mismo mapa que el emisor, anteponiendo el character_id de quien habló
// (así el cliente sabe a quién reproducirle, o de quién venía si más adelante se separa por
// hablante). El servidor no decodifica ni valida el audio — es un relay ciego.
func (r *Router) HandleBinaryMessage(characterID string, data []byte) {
	mapID := r.hub.MapOfCharacter(characterID)
	if mapID == "" || len(data) == 0 {
		return
	}
	framed := make([]byte, 0, characterIDLen+len(data))
	framed = append(framed, []byte(characterID)...)
	if len(characterID) < characterIDLen {
		framed = append(framed, make([]byte, characterIDLen-len(characterID))...) // padding, no debería pasar con UUIDs reales
	}
	framed = append(framed, data...)
	r.hub.BroadcastBinaryToMap(mapID, framed, characterID)
}

func (r *Router) HandleMessage(characterID string, env protocol.Envelope) {
	switch env.Type {
	case "move":
		r.handleMove(characterID, env)
	case "send_chat":
		r.handleChat(characterID, env)
	case "trade_request":
		r.handleTradeRequest(characterID, env)
	case "trade_accept":
		r.handleTradeAccept(characterID, env)
	case "trade_decline":
		r.handleTradeDecline(characterID, env)
	case "trade_offer_set":
		r.handleTradeOfferSet(characterID, env)
	case "trade_confirm":
		r.handleTradeConfirm(characterID, env)
	case "list_my_pokemon":
		r.handleListMyPokemon(characterID)
	case "friend_request":
		r.handleFriendRequest(characterID, env)
	case "friend_accept":
		r.handleFriendAccept(characterID, env)
	case "friend_decline":
		r.handleFriendDecline(characterID, env)
	case "friend_remove":
		r.handleFriendRemove(characterID, env)
	case "friend_list":
		r.handleFriendList(characterID)
	case "party_invite":
		r.handlePartyInvite(characterID, env)
	case "party_accept":
		r.handlePartyAccept(characterID, env)
	case "party_decline":
		r.handlePartyDecline(characterID, env)
	case "party_leave":
		r.handlePartyLeave(characterID, env)
	case "market_list":
		r.handleMarketList(characterID, env)
	case "market_cancel":
		r.handleMarketCancel(characterID, env)
	case "market_browse":
		r.handleMarketBrowse(characterID)
	case "market_my_listings":
		r.handleMarketMyListings(characterID)
	case "market_buy":
		r.handleMarketBuy(characterID, env)
	case "guild_create":
		r.handleGuildCreate(characterID, env)
	case "guild_invite":
		r.handleGuildInvite(characterID, env)
	case "guild_accept":
		r.handleGuildAccept(characterID, env)
	case "guild_decline":
		r.handleGuildDecline(characterID, env)
	case "guild_leave":
		r.handleGuildLeave(characterID)
	case "guild_kick":
		r.handleGuildKick(characterID, env)
	case "guild_info":
		r.handleGuildInfo(characterID)
	case "set_color":
		r.handleSetColor(characterID, env)
	case "battle_challenge":
		r.handleBattleChallenge(characterID, env)
	case "battle_accept":
		r.handleBattleAccept(characterID, env)
	case "battle_decline":
		r.handleBattleDecline(characterID, env)
	case "battle_action":
		r.handleBattleAction(characterID, env)
	default:
		slog.Warn("tipo de mensaje desconocido", "component", "router", "type", env.Type)
	}
}

func (r *Router) handleMove(characterID string, env protocol.Envelope) {
	var p protocol.MovePayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}

	// TODO producción: colisión básica contra el tilemap del servidor antes de aceptar la
	// posición (la velocidad ya se valida en Hub.UpdatePosition). No hay tilemap del lado
	// del servidor todavía, así que esto queda pendiente de una fuente de datos de mapas.

	oldMap, newMap, accepted, correctedX, correctedY := r.hub.UpdatePosition(characterID, p.MapID, p.X, p.Y, p.Facing, p.State)
	if !accepted {
		rejectedEnv, _ := protocol.NewEnvelope("move_rejected", protocol.MoveRejectedPayload{
			MapID: p.MapID, X: correctedX, Y: correctedY, Facing: p.Facing,
		})
		r.hub.SendTo(characterID, rejectedEnv)
		return
	}

	// Nickname/Color se resuelven acá (no llegan en el payload del cliente: son estado del
	// servidor) — antes handleMove los dejaba vacíos, y como es la ÚNICA notificación que
	// reciben los clientes remotos después del snapshot inicial, el nombre/color se veía en
	// blanco apenas el jugador se movía una vez (bug real, encontrado al armar las etiquetas
	// de nombre sobre los sprites remotos).
	nickname := r.hub.NicknameOfCharacter(characterID)
	color := r.hub.ColorOfCharacter(characterID)

	update, _ := protocol.NewEnvelope("player_update", protocol.PlayerUpdatePayload{
		CharacterID: characterID, Nickname: nickname, MapID: newMap, X: p.X, Y: p.Y, Facing: p.Facing, State: p.State, Color: color,
	})

	if oldMap != newMap {
		// Cambió de mapa: avisar salida al mapa viejo y entrada al nuevo,
		// en vez de solo un player_update, para que los clientes remotos
		// instancien/destruyan el sprite correctamente.
		leftEnv, _ := protocol.NewEnvelope("player_left_map", protocol.PlayerUpdatePayload{CharacterID: characterID, MapID: oldMap})
		r.hub.BroadcastToMap(oldMap, leftEnv, characterID)

		joinedEnv, _ := protocol.NewEnvelope("player_joined_map", protocol.PlayerUpdatePayload{
			CharacterID: characterID, Nickname: nickname, MapID: newMap, X: p.X, Y: p.Y, Facing: p.Facing, State: p.State, Color: color,
		})
		r.hub.BroadcastToMap(newMap, joinedEnv, characterID)
		return
	}

	r.hub.BroadcastToMap(newMap, update, characterID)
}

func (r *Router) handleChat(characterID string, env protocol.Envelope) {
	var p protocol.SendChatPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	r.chat.HandleSendChat(characterID, p)
}

func (r *Router) handleTradeRequest(characterID string, env protocol.Envelope) {
	var p protocol.TradeRequestPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	sessionID, err := r.trade.RequestTrade(characterID, p.TargetCharacterID)
	if err != nil {
		slog.Error("error creando sesión de trade", "component", "trade", "character_id", characterID, "error", err)
		return
	}
	notify, _ := protocol.NewEnvelope("trade_request_received", protocol.TradeRequestReceivedPayload{
		TradeSessionID: sessionID, FromCharacterID: characterID, FromNickname: r.hub.NicknameOfCharacter(characterID),
	})
	r.hub.SendTo(p.TargetCharacterID, notify)
}

func (r *Router) handleTradeAccept(characterID string, env protocol.Envelope) {
	var p protocol.TradeSessionRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if err := r.trade.AcceptTrade(p.TradeSessionID); err != nil {
		slog.Error("error aceptando trade", "component", "trade", "trade_session_id", p.TradeSessionID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	charA, charB, err := r.trade.Participants(p.TradeSessionID)
	if err != nil {
		slog.Error("error obteniendo participantes de trade", "component", "trade", "trade_session_id", p.TradeSessionID, "error", err)
		return
	}
	// Avisar a AMBOS (no solo al que aceptó): el que mandó el trade_request original no
	// tiene otra forma de enterarse de que ya puede pasar a la fase de ofertas.
	acceptedEnv, _ := protocol.NewEnvelope("trade_accepted", protocol.TradeSessionRefPayload{TradeSessionID: p.TradeSessionID})
	r.hub.SendTo(charA, acceptedEnv)
	r.hub.SendTo(charB, acceptedEnv)
}

func (r *Router) handleTradeDecline(characterID string, env protocol.Envelope) {
	var p protocol.TradeSessionRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	charA, charB, err := r.trade.Cancel(p.TradeSessionID, "declined")
	if err != nil {
		slog.Error("error declinando trade", "component", "trade", "trade_session_id", p.TradeSessionID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	cancelEnv, _ := protocol.NewEnvelope("trade_cancelled", protocol.TradeCancelledPayload{
		TradeSessionID: p.TradeSessionID, Reason: "declined",
	})
	r.hub.SendTo(charA, cancelEnv)
	r.hub.SendTo(charB, cancelEnv)
}

// HandleDisconnect libera cualquier trade activo del jugador que se desconecta,
// para que el Pokémon del otro jugador no quede bloqueado indefinidamente, y avisa a los
// demás jugadores de su mapa que se fue (sin esto, D.2b/clientes remotos mostrarían un
// marcador desactualizado indefinidamente). Debe correr ANTES de hub.Unregister — necesita
// que characterID siga registrado para poder leer su mapa/nickname (ver connection.go).
func (r *Router) HandleDisconnect(characterID string) {
	cancelled, err := r.trade.CancelActiveForCharacter(characterID, "disconnected")
	if err != nil {
		slog.Error("error cancelando sesiones activas al desconectar", "component", "trade", "character_id", characterID, "error", err)
	}
	for _, c := range cancelled {
		env, _ := protocol.NewEnvelope("trade_cancelled", protocol.TradeCancelledPayload{
			TradeSessionID: c.SessionID, Reason: "disconnected",
		})
		r.hub.SendTo(c.OtherCharID, env)
	}

	for _, otherCharID := range r.battle.CancelActiveForCharacter(characterID) {
		env, _ := protocol.NewEnvelope("battle_cancelled", protocol.BattleCancelledPayload{Reason: "disconnected"})
		r.hub.SendTo(otherCharID, env)
	}

	if mapID := r.hub.MapOfCharacter(characterID); mapID != "" {
		leftEnv, _ := protocol.NewEnvelope("player_left_map", protocol.PlayerUpdatePayload{
			CharacterID: characterID, MapID: mapID,
		})
		r.hub.BroadcastToMap(mapID, leftEnv, characterID)
	}

	r.leavePartyOnDisconnect(characterID)
	r.notifyFriendsOnlineStatus(characterID, false)
}

// HandleConnect corre justo después de que el jugador quedó registrado en el Hub.
// Avisa a sus amigos conectados que ahora está online, y le manda un snapshot de quién ya
// está en su mapa de spawn (sin esto, no vería a nadie hasta que esa persona se moviera).
func (r *Router) HandleConnect(characterID string) {
	r.notifyFriendsOnlineStatus(characterID, true)

	mapID := r.hub.MapOfCharacter(characterID)
	if mapID == "" {
		return
	}
	present := r.hub.PlayersInMap(mapID)
	others := present[:0]
	for _, p := range present {
		if p.CharacterID != characterID {
			others = append(others, p)
		}
	}
	if len(others) == 0 {
		return
	}
	env, _ := protocol.NewEnvelope("map_players_snapshot", protocol.MapPlayersSnapshotPayload{Players: others})
	r.hub.SendTo(characterID, env)
}

// SweepExpiredTrades cancela sesiones de trade abandonadas hace más de maxAge y
// notifica a ambos jugadores. Pensado para ser llamado periódicamente desde main.go.
func (r *Router) SweepExpiredTrades(maxAge time.Duration) {
	cancelled, err := r.trade.SweepExpired(maxAge)
	if err != nil {
		slog.Error("error en sweep de expiración de trades", "component", "trade", "error", err)
		return
	}
	for _, c := range cancelled {
		env, _ := protocol.NewEnvelope("trade_cancelled", protocol.TradeCancelledPayload{
			TradeSessionID: c.SessionID, Reason: "timeout",
		})
		r.hub.SendTo(c.OtherCharID, env)
	}
}

// ---- Amigos ----

func (r *Router) handleFriendRequest(characterID string, env protocol.Envelope) {
	var p protocol.FriendRequestPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	result, err := r.friends.Request(characterID, p.TargetUsername)
	if err != nil {
		slog.Error("error creando solicitud de amistad", "component", "friends", "character_id", characterID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	if result.ToCharacterID == "" {
		return // el destinatario no tiene personaje creado todavía; la solicitud queda pendiente en DB igual
	}
	notify, _ := protocol.NewEnvelope("friend_request_received", protocol.FriendRequestReceivedPayload{
		FromAccountID: result.FromAccountID, FromUsername: result.FromUsername,
	})
	r.hub.SendTo(result.ToCharacterID, notify)
}

func (r *Router) handleFriendAccept(characterID string, env protocol.Envelope) {
	var p protocol.FriendRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if _, err := r.friends.Accept(characterID, p.TargetAccountID); err != nil {
		slog.Error("error aceptando solicitud de amistad", "component", "friends", "character_id", characterID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	r.notifyFriendsOnlineStatus(characterID, true) // el que envió la solicitud original ahora ve al aceptante como amigo online
}

func (r *Router) handleFriendDecline(characterID string, env protocol.Envelope) {
	var p protocol.FriendRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if err := r.friends.Decline(characterID, p.TargetAccountID); err != nil {
		slog.Error("error declinando solicitud de amistad", "component", "friends", "character_id", characterID, "error", err)
	}
}

func (r *Router) handleFriendRemove(characterID string, env protocol.Envelope) {
	var p protocol.FriendRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if _, err := r.friends.Remove(characterID, p.TargetAccountID); err != nil {
		slog.Error("error eliminando amistad", "component", "friends", "character_id", characterID, "error", err)
	}
}

func (r *Router) handleFriendList(characterID string) {
	entries, err := r.friends.List(characterID)
	if err != nil {
		slog.Error("error listando amigos", "component", "friends", "character_id", characterID, "error", err)
		return
	}
	out := protocol.FriendListPayload{Friends: make([]protocol.FriendListEntryPayload, 0, len(entries))}
	for _, e := range entries {
		online := e.CharacterID != "" && r.hub.MapOfCharacter(e.CharacterID) != ""
		out.Friends = append(out.Friends, protocol.FriendListEntryPayload{
			AccountID: e.AccountID, Username: e.Username, Online: online,
		})
	}
	env, _ := protocol.NewEnvelope("friend_list", out)
	r.hub.SendTo(characterID, env)
}

// ---- Grupos (party) ----

func (r *Router) handlePartyInvite(characterID string, env protocol.Envelope) {
	var p protocol.PartyInvitePayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	partyID, err := r.party.Invite(characterID, p.TargetCharacterID)
	if err != nil {
		slog.Error("error invitando al grupo", "component", "party", "character_id", characterID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	notify, _ := protocol.NewEnvelope("party_invite_received", protocol.PartyInviteReceivedPayload{
		PartyID: partyID, FromCharacterID: characterID, FromNickname: r.hub.NicknameOfCharacter(characterID),
	})
	r.hub.SendTo(p.TargetCharacterID, notify)
}

func (r *Router) handlePartyAccept(characterID string, env protocol.Envelope) {
	var p protocol.PartyRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if err := r.party.Accept(characterID, p.PartyID); err != nil {
		slog.Error("error aceptando invitación al grupo", "component", "party", "character_id", characterID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	r.broadcastPartyUpdate(p.PartyID)
}

func (r *Router) handlePartyDecline(characterID string, env protocol.Envelope) {
	var p protocol.PartyRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if err := r.party.Decline(characterID, p.PartyID); err != nil {
		slog.Error("error declinando invitación al grupo", "component", "party", "character_id", characterID, "error", err)
	}
}

func (r *Router) handlePartyLeave(characterID string, env protocol.Envelope) {
	partyID, disbanded, err := r.party.Leave(characterID)
	if err != nil {
		slog.Error("error saliendo del grupo", "component", "party", "character_id", characterID, "error", err)
		return
	}
	if partyID == "" {
		return
	}
	if disbanded {
		disbandEnv, _ := protocol.NewEnvelope("party_disbanded", protocol.PartyDisbandedPayload{PartyID: partyID, Reason: "empty"})
		r.hub.SendTo(characterID, disbandEnv)
		return
	}
	r.broadcastPartyUpdate(partyID)
}

// leavePartyOnDisconnect libera al jugador de su grupo al desconectarse, para que
// el resto del grupo no quede esperando a alguien que ya no está.
func (r *Router) leavePartyOnDisconnect(characterID string) {
	partyID, disbanded, err := r.party.Leave(characterID)
	if err != nil {
		slog.Error("error limpiando grupo tras desconexión", "component", "party", "character_id", characterID, "error", err)
		return
	}
	if partyID == "" || disbanded {
		return
	}
	r.broadcastPartyUpdate(partyID)
}

func (r *Router) broadcastPartyUpdate(partyID string) {
	members, err := r.party.Members(partyID)
	if err != nil {
		slog.Error("error obteniendo miembros del grupo", "component", "party", "party_id", partyID, "error", err)
		return
	}
	payload := protocol.PartyUpdatePayload{PartyID: partyID}
	for _, m := range members {
		mapID, x, y, _ := r.hub.PositionOfCharacter(m.CharacterID)
		payload.Members = append(payload.Members, protocol.PartyMemberPayload{
			CharacterID: m.CharacterID, Nickname: r.hub.NicknameOfCharacter(m.CharacterID),
			MapID: mapID, X: x, Y: y, IsLeader: m.IsLeader,
		})
	}
	env, _ := protocol.NewEnvelope("party_update", payload)
	for _, m := range members {
		r.hub.SendTo(m.CharacterID, env)
	}
}

// notifyFriendsOnlineStatus avisa a los amigos ACEPTADOS y conectados de characterID
// que su estado online cambió, sin bloquear si la cuenta no tiene amigos.
func (r *Router) notifyFriendsOnlineStatus(characterID string, online bool) {
	entries, err := r.friends.List(characterID)
	if err != nil {
		slog.Error("error listando amigos para notificar estado", "component", "friends", "character_id", characterID, "error", err)
		return
	}
	accountID, err := r.friends.AccountIDForCharacter(characterID)
	if err != nil {
		return
	}
	env, _ := protocol.NewEnvelope("friend_status_update", protocol.FriendStatusUpdatePayload{
		AccountID: accountID, Online: online,
	})
	for _, e := range entries {
		if e.CharacterID != "" {
			r.hub.SendTo(e.CharacterID, env)
		}
	}
}

func (r *Router) handleTradeOfferSet(characterID string, env protocol.Envelope) {
	var p protocol.TradeOfferSetPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if err := r.trade.SetOffer(p.TradeSessionID, characterID, p.PokemonID); err != nil {
		slog.Error("error fijando oferta de trade", "component", "trade", "character_id", characterID, "trade_session_id", p.TradeSessionID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}

	// Avisar a AMBOS participantes qué se acaba de ofrecer: sin esto, cada uno tendría que
	// confirmar "a ciegas" sin ver qué le están por dar a cambio.
	summary, err := r.trade.GetSummary(p.PokemonID)
	if err != nil {
		slog.Error("error obteniendo resumen de pokémon ofrecido", "component", "trade", "pokemon_id", p.PokemonID, "error", err)
		return
	}
	charA, charB, err := r.trade.Participants(p.TradeSessionID)
	if err != nil {
		slog.Error("error obteniendo participantes de trade", "component", "trade", "trade_session_id", p.TradeSessionID, "error", err)
		return
	}
	updateEnv, _ := protocol.NewEnvelope("trade_offer_updated", protocol.TradeOfferUpdatedPayload{
		TradeSessionID: p.TradeSessionID, CharacterID: characterID,
		Pokemon: protocol.PokemonSummaryPayload{
			ID: summary.ID, SpeciesID: summary.SpeciesID, Nickname: summary.Nickname, Level: summary.Level, Location: summary.Location,
		},
	})
	r.hub.SendTo(charA, updateEnv)
	r.hub.SendTo(charB, updateEnv)
}

// handleListMyPokemon responde con el resumen de los Pokémon disponibles de characterID
// (usado por el cliente para poblar el selector de "qué ofrecer" en un trade).
func (r *Router) handleListMyPokemon(characterID string) {
	owned, err := r.trade.ListOwned(characterID)
	if err != nil {
		slog.Error("error listando pokémon propios", "component", "trade", "character_id", characterID, "error", err)
		return
	}
	out := protocol.MyPokemonListPayload{Pokemon: make([]protocol.PokemonSummaryPayload, 0, len(owned))}
	for _, p := range owned {
		out.Pokemon = append(out.Pokemon, protocol.PokemonSummaryPayload{
			ID: p.ID, SpeciesID: p.SpeciesID, Nickname: p.Nickname, Level: p.Level, Location: p.Location,
		})
	}
	env, _ := protocol.NewEnvelope("my_pokemon_list", out)
	r.hub.SendTo(characterID, env)
}

func (r *Router) handleTradeConfirm(characterID string, env protocol.Envelope) {
	var p protocol.TradeConfirmPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	result, err := r.trade.Confirm(p.TradeSessionID, characterID)
	if err != nil {
		slog.Error("error confirmando trade", "component", "trade", "character_id", characterID, "trade_session_id", p.TradeSessionID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	if result == nil {
		return // solo un jugador confirmó hasta ahora, falta el otro
	}

	// Notificar a AMBOS jugadores, cada uno con el ID del Pokémon que él recibió.
	doneForA, _ := protocol.NewEnvelope("trade_completed", protocol.TradeCompletedPayload{
		TradeSessionID: p.TradeSessionID, CharAReceivedID: result.CharAReceivedPokemonID,
	})
	doneForB, _ := protocol.NewEnvelope("trade_completed", protocol.TradeCompletedPayload{
		TradeSessionID: p.TradeSessionID, CharBReceivedID: result.CharBReceivedPokemonID,
	})
	r.hub.SendTo(result.CharAID, doneForA)
	r.hub.SendTo(result.CharBID, doneForB)
}

// ---- Batalla (PvP 1v1, ver server/internal/battlesession) ----

func (r *Router) handleBattleChallenge(characterID string, env protocol.Envelope) {
	var p protocol.BattleChallengePayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	sessionID := r.battle.Challenge(characterID, p.TargetCharacterID)
	notify, _ := protocol.NewEnvelope("battle_challenge_received", protocol.BattleChallengeReceivedPayload{
		BattleSessionID: sessionID, FromCharacterID: characterID, FromNickname: r.hub.NicknameOfCharacter(characterID),
	})
	r.hub.SendTo(p.TargetCharacterID, notify)
}

func (r *Router) handleBattleAccept(characterID string, env protocol.Envelope) {
	var p protocol.BattleSessionRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	charA, charB, viewA, viewB, err := r.battle.Accept(p.BattleSessionID)
	if err != nil {
		slog.Error("error aceptando batalla", "component", "battle", "battle_session_id", p.BattleSessionID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}

	// Cada participante recibe battle_start con SU propia perspectiva (yours/opponent).
	wireA := battlePokemonToWireCharacter(viewA)
	wireB := battlePokemonToWireCharacter(viewB)
	startForA, _ := protocol.NewEnvelope("battle_start", protocol.BattleStartPayload{
		BattleSessionID: p.BattleSessionID, Yours: wireA, Opponent: wireB,
	})
	startForB, _ := protocol.NewEnvelope("battle_start", protocol.BattleStartPayload{
		BattleSessionID: p.BattleSessionID, Yours: wireB, Opponent: wireA,
	})
	r.hub.SendTo(charA, startForA)
	r.hub.SendTo(charB, startForB)
}

func battlePokemonToWireCharacter(v battlesession.PokemonView) protocol.BattlePokemonPayload {
	return protocol.BattlePokemonPayload{
		PokemonID: v.PokemonID, SpeciesID: v.Species, Nickname: v.Nickname,
		Level: v.Level, CurrentHP: v.CurrentHP, MaxHP: v.MaxHP,
	}
}

func (r *Router) handleBattleDecline(characterID string, env protocol.Envelope) {
	var p protocol.BattleSessionRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	charA, charB, err := r.battle.Cancel(p.BattleSessionID)
	if err != nil {
		slog.Error("error declinando batalla", "component", "battle", "battle_session_id", p.BattleSessionID, "error", err)
		return
	}
	cancelEnv, _ := protocol.NewEnvelope("battle_cancelled", protocol.BattleCancelledPayload{
		BattleSessionID: p.BattleSessionID, Reason: "declined",
	})
	r.hub.SendTo(charA, cancelEnv)
	r.hub.SendTo(charB, cancelEnv)
}

func (r *Router) handleBattleAction(characterID string, env protocol.Envelope) {
	var p protocol.BattleActionPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	result, err := r.battle.SubmitAction(p.BattleSessionID, characterID, p.MoveSlot)
	if err != nil {
		slog.Error("error procesando acción de batalla", "component", "battle", "character_id", characterID, "battle_session_id", p.BattleSessionID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	if result == nil {
		return // falta la acción del rival, todavía no hay turno que reportar
	}

	events := make([]protocol.BattleEventPayload, 0, len(result.Events))
	for _, e := range result.Events {
		events = append(events, protocol.BattleEventPayload{
			Type: e.Type.String(), ActorCharacterID: e.ActorCharID,
			MoveID: e.MoveID, Damage: e.Damage, Effectiveness: e.Effectiveness, Fainted: e.Fainted,
		})
	}

	for charID, hp := range result.HPByCharacter {
		opponentHP := 0
		for otherID, otherHP := range result.HPByCharacter {
			if otherID != charID {
				opponentHP = otherHP
			}
		}
		turnEnv, _ := protocol.NewEnvelope("battle_turn_result", protocol.BattleTurnResultPayload{
			BattleSessionID: p.BattleSessionID, Events: events, YourHP: hp, OpponentHP: opponentHP,
		})
		r.hub.SendTo(charID, turnEnv)
	}

	if result.Finished {
		winnerEnv, _ := protocol.NewEnvelope("battle_end", protocol.BattleEndPayload{
			BattleSessionID: p.BattleSessionID, WinnerCharacterID: result.WinnerCharID, YouWon: true,
		})
		loserEnv, _ := protocol.NewEnvelope("battle_end", protocol.BattleEndPayload{
			BattleSessionID: p.BattleSessionID, WinnerCharacterID: result.WinnerCharID, YouWon: false,
		})
		r.hub.SendTo(result.WinnerCharID, winnerEnv)
		r.hub.SendTo(result.LoserCharID, loserEnv)
	}
}

// ---- Mercado (asincrónico) ----

func (r *Router) handleMarketList(characterID string, env protocol.Envelope) {
	var p protocol.MarketListPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	listingID, err := r.market.List(characterID, p.PokemonID, p.Price)
	if err != nil {
		slog.Error("error publicando en el mercado", "component", "market", "character_id", characterID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	okEnv, _ := protocol.NewEnvelope("market_list_ok", protocol.MarketListingRefPayload{ListingID: listingID})
	r.hub.SendTo(characterID, okEnv)
}

func (r *Router) handleMarketCancel(characterID string, env protocol.Envelope) {
	var p protocol.MarketListingRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if err := r.market.Cancel(p.ListingID, characterID); err != nil {
		slog.Error("error cancelando publicación de mercado", "component", "market", "listing_id", p.ListingID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	okEnv, _ := protocol.NewEnvelope("market_cancelled", protocol.MarketListingRefPayload{ListingID: p.ListingID})
	r.hub.SendTo(characterID, okEnv)
}

func (r *Router) handleMarketBrowse(characterID string) {
	listings, err := r.market.ListActive(50)
	if err != nil {
		slog.Error("error explorando el mercado", "component", "market", "error", err)
		return
	}
	r.hub.SendTo(characterID, marketListingsEnvelope("market_listings", listings))
}

func (r *Router) handleMarketMyListings(characterID string) {
	listings, err := r.market.MyListings(characterID)
	if err != nil {
		slog.Error("error listando publicaciones propias", "component", "market", "character_id", characterID, "error", err)
		return
	}
	// Tipo de mensaje distinto al de market_browse (aunque el payload tenga la misma forma):
	// así el cliente no tiene que llevar la cuenta de "cuál de las dos pedí último" para saber
	// qué hacer con la respuesta — la propia respuesta ya dice si son publicaciones propias o
	// de todos.
	r.hub.SendTo(characterID, marketListingsEnvelope("market_my_listings", listings))
}

func marketListingsEnvelope(msgType string, listings []market.Listing) protocol.Envelope {
	out := protocol.MarketListingsPayload{Listings: make([]protocol.MarketListingPayload, 0, len(listings))}
	for _, l := range listings {
		out.Listings = append(out.Listings, protocol.MarketListingPayload{
			ListingID: l.ID, SellerCharID: l.SellerCharID, SellerNickname: l.SellerNickname,
			Pokemon: protocol.PokemonSummaryPayload{ID: l.PokemonID, SpeciesID: l.SpeciesID, Nickname: l.PokemonName, Level: l.Level, Location: "in_trade"},
			Price:   l.Price,
		})
	}
	env, _ := protocol.NewEnvelope(msgType, out)
	return env
}

func (r *Router) handleMarketBuy(characterID string, env protocol.Envelope) {
	var p protocol.MarketListingRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	result, err := r.market.Buy(p.ListingID, characterID)
	if err != nil {
		slog.Error("error comprando en el mercado", "component", "market", "character_id", characterID, "listing_id", p.ListingID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}

	summary, err := r.trade.GetSummary(result.PokemonID)
	if err != nil {
		slog.Error("error obteniendo resumen de pokémon comprado", "component", "market", "pokemon_id", result.PokemonID, "error", err)
		return
	}
	purchasedEnv, _ := protocol.NewEnvelope("market_purchased", protocol.MarketPurchasedPayload{
		ListingID: p.ListingID, Price: result.Price,
		Pokemon: protocol.PokemonSummaryPayload{ID: summary.ID, SpeciesID: summary.SpeciesID, Nickname: summary.Nickname, Level: summary.Level, Location: summary.Location},
	})
	r.hub.SendTo(characterID, purchasedEnv)

	// El vendedor puede estar offline — SendTo simplemente no hace nada si no está conectado,
	// se entera la próxima vez que abra "mis publicaciones" (ya no la va a ver listada).
	soldEnv, _ := protocol.NewEnvelope("market_sold", protocol.MarketSoldPayload{
		ListingID: p.ListingID, BuyerNickname: r.hub.NicknameOfCharacter(characterID), Price: result.Price,
	})
	r.hub.SendTo(result.SellerCharID, soldEnv)
}

// ---- Gremios (persistentes) ----

func (r *Router) handleGuildCreate(characterID string, env protocol.Envelope) {
	var p protocol.GuildCreatePayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	guildID, err := r.guild.Create(characterID, p.Name)
	if err != nil {
		slog.Error("error creando gremio", "component", "guild", "character_id", characterID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	r.broadcastGuildUpdate(guildID)
}

func (r *Router) handleGuildInvite(characterID string, env protocol.Envelope) {
	var p protocol.GuildInvitePayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	guildID, err := r.guild.Invite(characterID, p.TargetCharacterID)
	if err != nil {
		slog.Error("error invitando al gremio", "component", "guild", "character_id", characterID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	name, err := r.guild.NameOf(guildID)
	if err != nil {
		slog.Error("error obteniendo nombre de gremio", "component", "guild", "guild_id", guildID, "error", err)
		return
	}
	notify, _ := protocol.NewEnvelope("guild_invite_received", protocol.GuildInviteReceivedPayload{
		GuildID: guildID, GuildName: name, FromCharacterID: characterID, FromNickname: r.hub.NicknameOfCharacter(characterID),
	})
	r.hub.SendTo(p.TargetCharacterID, notify)
}

func (r *Router) handleGuildAccept(characterID string, env protocol.Envelope) {
	var p protocol.GuildRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if err := r.guild.Accept(characterID, p.GuildID); err != nil {
		slog.Error("error aceptando invitación al gremio", "component", "guild", "character_id", characterID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	r.broadcastGuildUpdate(p.GuildID)
}

func (r *Router) handleGuildDecline(characterID string, env protocol.Envelope) {
	var p protocol.GuildRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if err := r.guild.Decline(characterID, p.GuildID); err != nil {
		slog.Error("error declinando invitación al gremio", "component", "guild", "character_id", characterID, "error", err)
	}
}

func (r *Router) handleGuildLeave(characterID string) {
	guildID, disbanded, err := r.guild.Leave(characterID)
	if err != nil {
		slog.Error("error saliendo del gremio", "component", "guild", "character_id", characterID, "error", err)
		return
	}
	if guildID == "" {
		return
	}
	if disbanded {
		disbandEnv, _ := protocol.NewEnvelope("guild_disbanded", protocol.GuildDisbandedPayload{GuildID: guildID, Reason: "empty"})
		r.hub.SendTo(characterID, disbandEnv)
		return
	}
	r.broadcastGuildUpdate(guildID)
}

func (r *Router) handleGuildKick(characterID string, env protocol.Envelope) {
	var p protocol.GuildKickPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	guildID, err := r.guild.Kick(characterID, p.TargetCharacterID)
	if err != nil {
		slog.Error("error expulsando del gremio", "component", "guild", "character_id", characterID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	kickedEnv, _ := protocol.NewEnvelope("guild_disbanded", protocol.GuildDisbandedPayload{GuildID: guildID, Reason: "kicked"})
	r.hub.SendTo(p.TargetCharacterID, kickedEnv)
	r.broadcastGuildUpdate(guildID)
}

// handleSetColor cambia el color de sprite del emisor: valida contra la paleta permitida,
// persiste en base (para que sobreviva a una reconexión) y difunde un player_update al mapa
// actual para que todos los que ya lo ven se enteren en el acto — el mismo patrón que un
// "move" normal, salvo que acá el trigger es un cambio de apariencia, no de posición.
func (r *Router) handleSetColor(characterID string, env protocol.Envelope) {
	var p protocol.SetColorPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if !AllowedSpriteColors[p.Color] {
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: "color no permitido"})
		r.hub.SendTo(characterID, errEnv)
		return
	}

	if err := r.character.SetColor(characterID, p.Color); err != nil {
		slog.Error("error guardando color de sprite", "component", "character", "character_id", characterID, "error", err)
		return
	}

	update, ok := r.hub.SetColor(characterID, p.Color)
	if !ok {
		return
	}
	env2, _ := protocol.NewEnvelope("player_update", update)
	r.hub.SendTo(characterID, env2) // confirmación al propio emisor (así su panel refleja el cambio ya guardado)
	r.hub.BroadcastToMap(update.MapID, env2, characterID)
}

// handleGuildInfo resuelve el gremio ACTUAL de characterID y le reenvía el guild_update
// completo. Necesario porque, a diferencia de party/trade (siempre arrancan vacíos), un
// gremio es persistente: si characterID ya pertenecía a uno antes de esta conexión (ej. cerró
// el cliente y volvió a entrar), nunca recibiría ese estado sin este pedido explícito — nada
// más lo empuja espontáneamente al reconectar.
func (r *Router) handleGuildInfo(characterID string) {
	guildID := r.guild.GuildOfCharacter(characterID)
	if guildID == "" {
		return
	}
	r.broadcastGuildUpdate(guildID)
}

func (r *Router) broadcastGuildUpdate(guildID string) {
	members, err := r.guild.Members(guildID)
	if err != nil {
		slog.Error("error obteniendo miembros del gremio", "component", "guild", "guild_id", guildID, "error", err)
		return
	}
	name, err := r.guild.NameOf(guildID)
	if err != nil {
		slog.Error("error obteniendo nombre del gremio", "component", "guild", "guild_id", guildID, "error", err)
		return
	}
	payload := protocol.GuildUpdatePayload{GuildID: guildID, Name: name}
	for _, m := range members {
		online := r.hub.MapOfCharacter(m.CharacterID) != ""
		payload.Members = append(payload.Members, protocol.GuildMemberPayload{
			CharacterID: m.CharacterID, Nickname: m.Nickname, Online: online, IsOfficer: m.IsOfficer,
		})
	}
	env, _ := protocol.NewEnvelope("guild_update", payload)
	for _, m := range members {
		r.hub.SendTo(m.CharacterID, env)
	}
}
