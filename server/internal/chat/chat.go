package chat

import (
	"log/slog"
	"time"

	"pokemon-online/server/internal/protocol"
)

// PlayerLookup lo implementa el paquete de más alto nivel para resolver datos
// del jugador (nickname, mapa actual) sin que chat dependa directamente de ws/db.
type PlayerLookup interface {
	NicknameOf(characterID string) string
	MapOf(characterID string) string
}

// Broadcaster abstrae el hub para no acoplar chat a la implementación de websockets.
type Broadcaster interface {
	BroadcastToMap(mapID string, env protocol.Envelope, excludeCharacterID string)
	BroadcastGlobal(env protocol.Envelope, excludeCharacterID string)
	SendTo(characterID string, env protocol.Envelope) bool
}

// RateLimiter decide si el jugador puede enviar otro mensaje de chat ahora mismo.
// Implementado con Redis (ver internal/ratelimit) para que el límite sea compartido
// entre todas las instancias del servidor, no solo memoria de un proceso.
type RateLimiter interface {
	Allow(characterID string) (bool, error)
}

// GuildLookup resuelve los miembros del gremio de un personaje, para el canal "guild".
// Puede ser nil (el canal "guild" simplemente no hace nada sin él) — chat no debería dejar
// de funcionar por completo si en algún arranque no se inyecta gremios.
type GuildLookup interface {
	MembersOf(characterID string) []string
}

type Service struct {
	lookup  PlayerLookup
	broad   Broadcaster
	limiter RateLimiter
	guilds  GuildLookup
}

func NewService(lookup PlayerLookup, broad Broadcaster, limiter RateLimiter, guilds GuildLookup) *Service {
	return &Service{lookup: lookup, broad: broad, limiter: limiter, guilds: guilds}
}

// HandleSendChat procesa un mensaje entrante y lo redistribuye según el canal.
func (s *Service) HandleSendChat(fromCharacterID string, payload protocol.SendChatPayload) {
	if s.limiter != nil {
		allowed, err := s.limiter.Allow(fromCharacterID)
		if err != nil {
			// Redis no disponible: falla abierto (deja pasar el mensaje) en vez de
			// tumbar el chat entero por un problema de infraestructura secundaria.
			slog.Warn("error consultando rate limiter", "component", "chat", "character_id", fromCharacterID, "error", err)
		} else if !allowed {
			errEnv, _ := protocol.NewEnvelope("error", protocol.ErrorPayload{
				Code: "rate_limited", Message: "estás enviando mensajes demasiado rápido",
			})
			s.broad.SendTo(fromCharacterID, errEnv)
			return
		}
	}

	out := protocol.ChatMessagePayload{
		Channel:         payload.Channel,
		FromCharacterID: fromCharacterID,
		FromNickname:    s.lookup.NicknameOf(fromCharacterID),
		Message:         payload.Message,
		Timestamp:       time.Now().UTC().Format(time.RFC3339),
	}
	env, err := protocol.NewEnvelope("chat_message", out)
	if err != nil {
		return
	}

	switch payload.Channel {
	case "global":
		s.broad.BroadcastGlobal(env, "")
	case "local":
		mapID := s.lookup.MapOf(fromCharacterID)
		s.broad.BroadcastToMap(mapID, env, "")
	case "private":
		s.broad.SendTo(payload.TargetCharacterID, env)
		s.broad.SendTo(fromCharacterID, env) // eco al emisor para que vea su propio mensaje
	case "guild":
		if s.guilds == nil {
			return
		}
		for _, memberID := range s.guilds.MembersOf(fromCharacterID) {
			s.broad.SendTo(memberID, env) // MembersOf ya incluye al emisor, no hace falta un SendTo aparte
		}
	case "command":
		// El parseo de comandos (/trade, /msg, /help...) se resuelve en una capa
		// superior que intercepta este canal antes de llegar aquí, o se despacha
		// desde este mismo switch a los servicios correspondientes (trade, social).
	}
}
