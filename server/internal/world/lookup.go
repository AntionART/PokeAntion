package world

import (
	"pokemon-online/server/internal/social"
	"pokemon-online/server/internal/ws"
)

// HubLookup adapta ws.Hub a la interfaz chat.PlayerLookup, para no acoplar
// el paquete chat directamente a la implementación del Hub.
type HubLookup struct {
	hub *ws.Hub
}

func NewHubLookup(hub *ws.Hub) *HubLookup {
	return &HubLookup{hub: hub}
}

func (l *HubLookup) NicknameOf(characterID string) string {
	return l.hub.NicknameOfCharacter(characterID)
}

func (l *HubLookup) MapOf(characterID string) string {
	return l.hub.MapOfCharacter(characterID)
}

// GuildLookup adapta social.GuildService a la interfaz chat.GuildLookup, para el canal de
// chat "guild" (ver internal/chat) — mismo motivo que HubLookup: chat no debería depender
// directamente de cómo se resuelven los miembros de un gremio.
type GuildLookup struct {
	guilds *social.GuildService
}

func NewGuildLookup(guilds *social.GuildService) *GuildLookup {
	return &GuildLookup{guilds: guilds}
}

// MembersOf devuelve TODOS los miembros del gremio de characterID, incluyéndolo a él mismo
// (así el canal "guild" hace eco al emisor, igual que "private" — ver chat.HandleSendChat).
// Devuelve nil si no está en ningún gremio.
func (l *GuildLookup) MembersOf(characterID string) []string {
	guildID := l.guilds.GuildOfCharacter(characterID)
	if guildID == "" {
		return nil
	}
	members, err := l.guilds.Members(guildID)
	if err != nil {
		return nil
	}
	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.CharacterID
	}
	return ids
}
