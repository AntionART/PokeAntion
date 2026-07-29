package world

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"pokemon-online/server/internal/battle"
	"pokemon-online/server/internal/battlesession"
	"pokemon-online/server/internal/character"
	"pokemon-online/server/internal/chat"
	"pokemon-online/server/internal/inventory"
	"pokemon-online/server/internal/market"
	"pokemon-online/server/internal/pokemon"
	"pokemon-online/server/internal/protocol"
	"pokemon-online/server/internal/social"
	"pokemon-online/server/internal/trade"
	"pokemon-online/server/internal/wildencounter"
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
	inventory *inventory.Service
	wild      *wildencounter.Service
	pokemon   *pokemon.Service
}

func NewRouter(hub *ws.Hub, chatSvc *chat.Service, tradeSvc *trade.Service, friendsSvc *social.Service, partySvc *social.PartyService, marketSvc *market.Service, guildSvc *social.GuildService, characterSvc *character.Service, battleSvc *battlesession.Service, inventorySvc *inventory.Service, wildSvc *wildencounter.Service, pokemonSvc *pokemon.Service) *Router {
	return &Router{hub: hub, chat: chatSvc, trade: tradeSvc, friends: friendsSvc, party: partySvc, market: marketSvc, guild: guildSvc, character: characterSvc, battle: battleSvc, inventory: inventorySvc, wild: wildSvc, pokemon: pokemonSvc}
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
	case "list_my_items":
		r.handleListMyItems(characterID)
	case "shop_catalog_request":
		r.handleShopCatalogRequest(characterID)
	case "buy_item":
		r.handleBuyItem(characterID, env)
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
	case "battle_switch":
		r.handleBattleSwitch(characterID, env)
	case "battle_item":
		r.handleBattleItem(characterID, env)
	case "battle_flee":
		r.handleBattleFlee(characterID, env)
	case "battle_team_request":
		r.handleBattleTeamRequest(characterID, env)
	case "wild_encounter_triggered":
		r.handleWildEncounterTriggered(characterID, env)
	case "wild_action":
		r.handleWildAction(characterID, env)
	case "wild_throw_ball":
		r.handleWildThrowBall(characterID, env)
	case "wild_flee":
		r.handleWildFlee(characterID, env)
	case "learn_move_decision":
		r.handleLearnMoveDecision(characterID, env)
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

	r.wild.CancelActiveForCharacter(characterID) // nadie más a quien avisar, ver comentario del método

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

// handleListMyItems responde con los objetos de curación reales que tiene characterID (ver
// server/internal/inventory) — usado para poblar el menú Bag de una batalla, no una lista fija.
func (r *Router) handleListMyItems(characterID string) {
	stacks, err := r.inventory.List(characterID)
	if err != nil {
		slog.Error("error listando objetos propios", "component", "inventory", "character_id", characterID, "error", err)
		return
	}
	out := protocol.MyItemListPayload{Items: make([]protocol.ItemStackPayload, 0, len(stacks))}
	for _, st := range stacks {
		info, ok := inventory.Catalog[st.ItemID]
		if ok && info.Pocket == "key_items" {
			continue // llaves (cañas de pescar, etc.) no son un objeto de Bag usable en batalla
		}
		name := info.Name
		if !ok {
			name = fmt.Sprintf("#%d", st.ItemID)
		}
		out.Items = append(out.Items, protocol.ItemStackPayload{ItemID: st.ItemID, Name: name, Quantity: st.Quantity})
	}
	env, _ := protocol.NewEnvelope("my_item_list", out)
	r.hub.SendTo(characterID, env)
}

// ---- Tienda (panel simple siempre accesible — ver protocol.ShopCatalogPayload/BuyItemPayload)
// No hay NPC/edificio de Pokemart todavía: es una superficie de UI propia del cliente, como el
// panel de amigos/grupo (F5), no algo atado a una posición del mapa.

// handleShopCatalogRequest responde con el catálogo comprable real (nombre + precio de
// inventory.Catalog) — nunca hardcodeado en el cliente, para que un precio nunca pueda
// desincronizarse entre los dos lados.
func (r *Router) handleShopCatalogRequest(characterID string) {
	items := inventory.PurchasableItems()
	out := protocol.ShopCatalogPayload{Items: make([]protocol.ShopItemPayload, 0, len(items))}
	for _, info := range items {
		out.Items = append(out.Items, protocol.ShopItemPayload{ItemID: info.ID, Name: info.Name, Price: info.Price})
	}
	env, _ := protocol.NewEnvelope("shop_catalog", out)
	r.hub.SendTo(characterID, env)
}

// handleBuyItem cobra Quantity*precio (precio SIEMPRE resuelto server-side, nunca confiado del
// cliente) y acredita el inventario si el débito de dinero funcionó. El débito es atómico
// (character.TryDebitMoney, un solo UPDATE...WHERE) así que no hay ventana para que dos compras
// concurrentes del mismo personaje dejen dinero negativo.
func (r *Router) handleBuyItem(characterID string, env protocol.Envelope) {
	var p protocol.BuyItemPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if p.Quantity <= 0 || p.Quantity > 99 {
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: "cantidad inválida (1-99)"})
		r.hub.SendTo(characterID, errEnv)
		return
	}

	info, ok := inventory.Catalog[p.ItemID]
	if !ok || info.Price <= 0 {
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: "ese objeto no está en venta"})
		r.hub.SendTo(characterID, errEnv)
		return
	}

	totalCost := info.Price * p.Quantity
	newMoney, err := r.character.TryDebitMoney(characterID, totalCost)
	if err != nil {
		if err == character.ErrInsufficientFunds {
			errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: "no te alcanza el dinero"})
			r.hub.SendTo(characterID, errEnv)
			return
		}
		slog.Error("error cobrando compra", "component", "inventory", "character_id", characterID, "error", err)
		return
	}

	if err := r.inventory.Grant(characterID, p.ItemID, p.Quantity); err != nil {
		// El dinero ya se descontó — esto es un fallo real de infraestructura (Postgres caído a
		// mitad de la operación), no algo que se pueda revertir limpio sin una transacción
		// explícita (que este proyecto no usa en ningún otro lado, ver market/trade). Se loguea
		// fuerte para que quien hostee el server lo note y pueda compensar a mano si hace falta.
		slog.Error("compra cobrada pero no se pudo acreditar el objeto", "component", "inventory",
			"character_id", characterID, "item_id", p.ItemID, "quantity", p.Quantity, "error", err)
		return
	}

	newQuantity := 0
	if stacks, err := r.inventory.List(characterID); err == nil {
		for _, st := range stacks {
			if st.ItemID == p.ItemID {
				newQuantity = st.Quantity
				break
			}
		}
	}

	resultEnv, _ := protocol.NewEnvelope("buy_result", protocol.BuyResultPayload{
		ItemID: p.ItemID, Quantity: p.Quantity, TotalCost: totalCost, NewMoney: newMoney, NewQuantity: newQuantity,
	})
	r.hub.SendTo(characterID, resultEnv)
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
	r.submitBattleAction(characterID, p.BattleSessionID, battlesession.ActionRequest{MoveSlot: p.MoveSlot})
}

func (r *Router) handleBattleSwitch(characterID string, env protocol.Envelope) {
	var p protocol.BattleSwitchPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	r.submitBattleAction(characterID, p.BattleSessionID, battlesession.ActionRequest{IsSwitch: true, TeamSlot: p.TeamSlot})
}

func (r *Router) handleBattleItem(characterID string, env protocol.Envelope) {
	var p protocol.BattleItemPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	r.submitBattleAction(characterID, p.BattleSessionID, battlesession.ActionRequest{IsItem: true, ItemID: p.ItemID, TeamSlot: p.TeamSlot})
}

// submitBattleAction es el camino compartido por battle_action/battle_switch: ambos mandan una
// ActionRequest a battlesession y, si el intercambio quedó resuelto (el rival ya había mandado
// la suya), reportan el mismo battle_turn_result/battle_end — la única diferencia entre
// atacar y cambiar de Pokémon es CÓMO se arma la ActionRequest, no qué se hace con el resultado.
func (r *Router) submitBattleAction(characterID, sessionID string, req battlesession.ActionRequest) {
	result, err := r.battle.SubmitAction(sessionID, characterID, req)
	if err != nil {
		slog.Error("error procesando acción de batalla", "component", "battle", "character_id", characterID, "battle_session_id", sessionID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	if result == nil {
		return // falta la acción del rival, todavía no hay intercambio que reportar
	}

	events := make([]protocol.BattleEventPayload, 0, len(result.Events))
	for _, e := range result.Events {
		events = append(events, protocol.BattleEventPayload{
			Type: e.Type.String(), ActorCharacterID: e.ActorCharID,
			MoveID: e.MoveID, Damage: e.Damage, Effectiveness: e.Effectiveness, Fainted: e.Fainted,
			Amount: e.Amount, TargetSpecies: e.TargetSpecies, TargetNickname: e.TargetNickname, ItemID: e.ItemID,
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
			BattleSessionID: sessionID, Events: events, YourHP: hp, OpponentHP: opponentHP,
			YouMustSwitch: result.NeedsSwitch == charID,
		})
		r.hub.SendTo(charID, turnEnv)
	}

	if result.Finished {
		winnerEnv, _ := protocol.NewEnvelope("battle_end", protocol.BattleEndPayload{
			BattleSessionID: sessionID, WinnerCharacterID: result.WinnerCharID, YouWon: true, Reason: result.Reason,
		})
		loserEnv, _ := protocol.NewEnvelope("battle_end", protocol.BattleEndPayload{
			BattleSessionID: sessionID, WinnerCharacterID: result.WinnerCharID, YouWon: false, Reason: result.Reason,
		})
		r.hub.SendTo(result.WinnerCharID, winnerEnv)
		r.hub.SendTo(result.LoserCharID, loserEnv)
	}
}

// handleBattleFlee termina la batalla de inmediato a favor del rival — ver
// battlesession.Service.Flee: a diferencia de atacar/cambiar, huir no espera a que el rival
// también mande su jugada.
func (r *Router) handleBattleFlee(characterID string, env protocol.Envelope) {
	var p protocol.BattleSessionRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	winner, loser, err := r.battle.Flee(p.BattleSessionID, characterID)
	if err != nil {
		slog.Error("error huyendo de la batalla", "component", "battle", "character_id", characterID, "battle_session_id", p.BattleSessionID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	winnerEnv, _ := protocol.NewEnvelope("battle_end", protocol.BattleEndPayload{
		BattleSessionID: p.BattleSessionID, WinnerCharacterID: winner, YouWon: true, Reason: "fled",
	})
	loserEnv, _ := protocol.NewEnvelope("battle_end", protocol.BattleEndPayload{
		BattleSessionID: p.BattleSessionID, WinnerCharacterID: winner, YouWon: false, Reason: "fled",
	})
	r.hub.SendTo(winner, winnerEnv)
	r.hub.SendTo(loser, loserEnv)
}

// handleBattleTeamRequest responde con el equipo completo del emisor dentro de una batalla
// (para el menú "Pokémon" del cliente) — battle_start solo manda el activo, no todo el equipo.
func (r *Router) handleBattleTeamRequest(characterID string, env protocol.Envelope) {
	var p protocol.BattleTeamRequestPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	team, activeIdx, err := r.battle.Team(p.BattleSessionID, characterID)
	if err != nil {
		slog.Error("error obteniendo equipo de batalla", "component", "battle", "character_id", characterID, "battle_session_id", p.BattleSessionID, "error", err)
		return
	}
	wireTeam := make([]protocol.BattlePokemonPayload, 0, len(team))
	for _, v := range team {
		wireTeam = append(wireTeam, battlePokemonToWireCharacter(v))
	}
	env2, _ := protocol.NewEnvelope("battle_team", protocol.BattleTeamPayload{
		BattleSessionID: p.BattleSessionID, Team: wireTeam, ActiveIndex: activeIdx,
	})
	r.hub.SendTo(characterID, env2)
}

// ---- Encuentros salvajes (ver server/internal/wildencounter) ----

// handleWildEncounterTriggered arranca un encuentro cuando el cliente detecta (por RAM) que la
// ROM disparó uno nativo — el mapa NO se toma del payload del cliente, se usa
// hub.MapOfCharacter (lo que el propio Hub ya sabe por los "move" de este personaje): un solo
// canal de verdad para "en qué mapa estás", no dos que podrían desincronizarse.
func (r *Router) handleWildEncounterTriggered(characterID string, env protocol.Envelope) {
	var p protocol.WildEncounterTriggeredPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	mapID := r.hub.MapOfCharacter(characterID)
	if mapID == "" {
		return
	}
	kind := wildencounter.EncounterKind(p.EncounterType)
	if kind == "" {
		kind = wildencounter.EncounterLand // default: mismo comportamiento que antes de que existiera este campo
	}
	sessionID, playerView, wildView, err := r.wild.StartEncounter(characterID, mapID, kind, p.RodTier, rand.New(rand.NewSource(time.Now().UnixNano())))
	if err != nil {
		if err != wildencounter.ErrNoEncounterHere {
			// ErrInvalidRodTier/ErrMissingRod SÍ son errores que el cliente necesita ver (le
			// falta un objeto real o mandó un valor inválido) — a diferencia de "no tocó
			// encuentro esta vez", que es silencioso.
			if err == wildencounter.ErrInvalidRodTier || err == wildencounter.ErrMissingRod {
				errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
				r.hub.SendTo(characterID, errEnv)
				return
			}
			slog.Error("error iniciando encuentro salvaje", "component", "wildencounter", "character_id", characterID, "map_id", mapID, "error", err)
		}
		return // sin encuentro esta vez (o sin tabla para este mapa) — no es un error que el cliente necesite ver
	}

	startEnv, _ := protocol.NewEnvelope("wild_battle_start", protocol.WildBattleStartPayload{
		SessionID: sessionID,
		Yours: protocol.BattlePokemonPayload{
			PokemonID: playerView.PokemonID, SpeciesID: playerView.Species, Nickname: playerView.Nickname,
			Level: playerView.Level, CurrentHP: playerView.CurrentHP, MaxHP: playerView.MaxHP,
		},
		Wild: protocol.WildPokemonPayload{SpeciesID: wildView.Species, Level: wildView.Level, CurrentHP: wildView.CurrentHP, MaxHP: wildView.MaxHP},
	})
	r.hub.SendTo(characterID, startEnv)
}

func (r *Router) handleWildAction(characterID string, env protocol.Envelope) {
	var p protocol.WildActionPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	result, err := r.wild.SubmitMove(p.SessionID, characterID, p.MoveSlot)
	if err != nil {
		slog.Error("error procesando acción de encuentro salvaje", "component", "wildencounter", "character_id", characterID, "session_id", p.SessionID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	r.sendWildTurnResult(characterID, p.SessionID, result)
}

func (r *Router) handleWildThrowBall(characterID string, env protocol.Envelope) {
	var p protocol.WildThrowBallPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	result, err := r.wild.ThrowBall(p.SessionID, characterID, p.ItemID)
	if err != nil {
		slog.Error("error tirando Poké Ball", "component", "wildencounter", "character_id", characterID, "session_id", p.SessionID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
		return
	}
	r.sendWildTurnResult(characterID, p.SessionID, result)
}

func (r *Router) handleWildFlee(characterID string, env protocol.Envelope) {
	var p protocol.WildSessionRefPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if err := r.wild.Flee(p.SessionID, characterID); err != nil {
		slog.Error("error huyendo de encuentro salvaje", "component", "wildencounter", "character_id", characterID, "session_id", p.SessionID, "error", err)
		return
	}
	endEnv, _ := protocol.NewEnvelope("wild_battle_end", protocol.WildBattleEndPayload{SessionID: p.SessionID, Reason: "fled"})
	r.hub.SendTo(characterID, endEnv)
}

// sendWildTurnResult reporta el resultado de un intercambio (SubmitMove/ThrowBall comparten
// forma) — si terminó, manda wild_battle_end con el detalle correspondiente (experiencia
// ganada, o el Pokémon recién atrapado).
func (r *Router) sendWildTurnResult(characterID, sessionID string, result *wildencounter.TurnResult) {
	events := make([]protocol.WildEventPayload, 0, len(result.Events))
	for _, e := range result.Events {
		events = append(events, protocol.WildEventPayload{
			Type: e.Type.String(), IsPlayer: e.IsPlayer,
			MoveID: e.MoveID, Damage: e.Damage, Effectiveness: e.Effectiveness, Fainted: e.Fainted, Amount: e.Amount,
		})
	}
	turnEnv, _ := protocol.NewEnvelope("wild_turn_result", protocol.WildTurnResultPayload{
		SessionID: sessionID, Events: events, YourHP: result.PlayerHP, WildHP: result.WildHP,
	})
	r.hub.SendTo(characterID, turnEnv)

	if !result.Finished {
		return
	}
	endPayload := protocol.WildBattleEndPayload{
		SessionID: sessionID, Reason: result.Reason,
		ExpGained: result.ExpGained, LeveledUp: result.LeveledUp, NewLevel: result.NewLevel,
		LearnedMoveIds: result.LearnedMoves,
	}
	if result.CaughtPokemon != nil {
		endPayload.CaughtPokemon = &protocol.PokemonSummaryPayload{
			ID: result.CaughtPokemon.ID, SpeciesID: result.CaughtPokemon.Species,
			Nickname: result.CaughtPokemon.Nickname, Level: result.CaughtPokemon.Level, Location: "team",
		}
	}
	endEnv, _ := protocol.NewEnvelope("wild_battle_end", endPayload)
	r.hub.SendTo(characterID, endEnv)

	// Prompts de "reemplazar movimiento" van APARTE de wild_battle_end (no bloquean el fin de
	// la pelea) — uno por movimiento que no tuvo lugar, el cliente los procesa en orden.
	for _, pending := range result.PendingMoveLearns {
		promptEnv, _ := protocol.NewEnvelope("wild_move_replace_prompt", protocol.WildMoveReplacePromptPayload{
			PokemonID: pending.PokemonID, NewMoveID: pending.NewMoveID, CurrentMoveIds: pending.CurrentMoveIDs,
		})
		r.hub.SendTo(characterID, promptEnv)
	}
}

// handleLearnMoveDecision procesa la respuesta del jugador a wild_move_replace_prompt.
// ReplaceSlot -1 = declinar (no se toca nada, el movimiento nuevo se pierde, igual que el
// juego real si el jugador dice que no). El ownership de PokemonID se valida DENTRO de
// pokemon.ReplaceMove, no acá — ver el comentario en esa función.
func (r *Router) handleLearnMoveDecision(characterID string, env protocol.Envelope) {
	var p protocol.LearnMoveDecisionPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return
	}
	if p.ReplaceSlot < 0 {
		return // el jugador declinó aprender el movimiento nuevo
	}
	pp := 0
	if m, ok := battle.MoveByID(p.NewMoveID); ok {
		pp = m.PP
	}
	if err := r.pokemon.ReplaceMove(characterID, p.PokemonID, p.ReplaceSlot, p.NewMoveID, pp); err != nil {
		slog.Error("error reemplazando movimiento", "component", "pokemon", "character_id", characterID, "pokemon_id", p.PokemonID, "error", err)
		errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{Code: "invalid_state", Message: err.Error()})
		r.hub.SendTo(characterID, errEnv)
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
