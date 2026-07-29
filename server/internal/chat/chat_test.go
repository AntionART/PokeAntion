package chat

import (
	"encoding/json"
	"errors"
	"testing"

	"pokemon-online/server/internal/protocol"
)

var errBoom = errors.New("redis no disponible (simulado)")

// Tests unitarios puros (sin Postgres): chat.Service solo depende de interfaces
// (PlayerLookup/Broadcaster/RateLimiter/GuildLookup), así que se prueban con fakes en
// memoria — no hay estado compartido real que un mock no pueda representar acá, a diferencia
// de trade/social/market.

type fakeLookup struct {
	nicknames map[string]string
	maps      map[string]string
}

func (f *fakeLookup) NicknameOf(characterID string) string { return f.nicknames[characterID] }
func (f *fakeLookup) MapOf(characterID string) string       { return f.maps[characterID] }

type sentMessage struct {
	kind   string // "map" | "global" | "to"
	target string // mapID (map), "" (global), o characterID (to)
	env    protocol.Envelope
}

type fakeBroadcaster struct {
	sent []sentMessage
}

func (f *fakeBroadcaster) BroadcastToMap(mapID string, env protocol.Envelope, excludeCharacterID string) {
	f.sent = append(f.sent, sentMessage{kind: "map", target: mapID, env: env})
}
func (f *fakeBroadcaster) BroadcastGlobal(env protocol.Envelope, excludeCharacterID string) {
	f.sent = append(f.sent, sentMessage{kind: "global", env: env})
}
func (f *fakeBroadcaster) SendTo(characterID string, env protocol.Envelope) bool {
	f.sent = append(f.sent, sentMessage{kind: "to", target: characterID, env: env})
	return true
}

type fakeLimiter struct {
	allow bool
	err   error
}

func (f *fakeLimiter) Allow(characterID string) (bool, error) { return f.allow, f.err }

type fakeGuildLookup struct {
	members map[string][]string
}

func (f *fakeGuildLookup) MembersOf(characterID string) []string { return f.members[characterID] }

func newTestService(limiter RateLimiter, guilds GuildLookup) (*Service, *fakeBroadcaster) {
	lookup := &fakeLookup{
		nicknames: map[string]string{"ash": "Ash", "misty": "Misty"},
		maps:      map[string]string{"ash": "MAP_ROUTE101", "misty": "MAP_ROUTE101"},
	}
	broad := &fakeBroadcaster{}
	return NewService(lookup, broad, limiter, guilds), broad
}

func decodeChatMessage(t *testing.T, env protocol.Envelope) protocol.ChatMessagePayload {
	t.Helper()
	var out protocol.ChatMessagePayload
	if err := json.Unmarshal(env.Payload, &out); err != nil {
		t.Fatalf("decodificando payload de %q: %v", env.Type, err)
	}
	return out
}

func TestChat_LocalBroadcastsToSenderMap(t *testing.T) {
	svc, broad := newTestService(nil, nil)

	svc.HandleSendChat("ash", protocol.SendChatPayload{Channel: "local", Message: "hola"})

	if len(broad.sent) != 1 {
		t.Fatalf("mensajes enviados = %d, esperaba 1", len(broad.sent))
	}
	msg := broad.sent[0]
	if msg.kind != "map" || msg.target != "MAP_ROUTE101" {
		t.Errorf("mensaje local = %+v, esperaba broadcast al mapa MAP_ROUTE101", msg)
	}
	payload := decodeChatMessage(t, msg.env)
	if payload.FromNickname != "Ash" || payload.Message != "hola" {
		t.Errorf("payload = %+v, no coincide con lo esperado", payload)
	}
}

func TestChat_GlobalBroadcastsToEveryone(t *testing.T) {
	svc, broad := newTestService(nil, nil)

	svc.HandleSendChat("ash", protocol.SendChatPayload{Channel: "global", Message: "hola a todos"})

	if len(broad.sent) != 1 || broad.sent[0].kind != "global" {
		t.Errorf("mensajes enviados = %+v, esperaba 1 broadcast global", broad.sent)
	}
}

func TestChat_PrivateEchoesToSenderAndTarget(t *testing.T) {
	svc, broad := newTestService(nil, nil)

	svc.HandleSendChat("ash", protocol.SendChatPayload{Channel: "private", TargetCharacterID: "misty", Message: "hola misty"})

	if len(broad.sent) != 2 {
		t.Fatalf("mensajes enviados = %d, esperaba 2 (destinatario + eco al emisor)", len(broad.sent))
	}
	targets := map[string]bool{broad.sent[0].target: true, broad.sent[1].target: true}
	if !targets["misty"] || !targets["ash"] {
		t.Errorf("destinos = %+v, esperaba tanto misty como ash", targets)
	}
}

func TestChat_GuildBroadcastsToMembersOnly(t *testing.T) {
	guilds := &fakeGuildLookup{members: map[string][]string{"ash": {"ash", "misty"}}}
	svc, broad := newTestService(nil, guilds)

	svc.HandleSendChat("ash", protocol.SendChatPayload{Channel: "guild", Message: "hola gremio"})

	if len(broad.sent) != 2 {
		t.Fatalf("mensajes enviados = %d, esperaba 2 (uno por miembro)", len(broad.sent))
	}
}

func TestChat_GuildChannelNoOpsWithoutGuildLookup(t *testing.T) {
	svc, broad := newTestService(nil, nil) // guilds = nil

	svc.HandleSendChat("ash", protocol.SendChatPayload{Channel: "guild", Message: "hola"})

	if len(broad.sent) != 0 {
		t.Errorf("mensajes enviados = %+v, esperaba ninguno (sin GuildLookup no debe romper)", broad.sent)
	}
}

func TestChat_RateLimitedBlocksMessage(t *testing.T) {
	svc, broad := newTestService(&fakeLimiter{allow: false}, nil)

	svc.HandleSendChat("ash", protocol.SendChatPayload{Channel: "global", Message: "flood"})

	if len(broad.sent) != 1 || broad.sent[0].kind != "to" || broad.sent[0].target != "ash" {
		t.Fatalf("tras rate limit = %+v, esperaba un solo error mandado a ash", broad.sent)
	}
	var errPayload protocol.ErrorPayload
	if err := json.Unmarshal(broad.sent[0].env.Payload, &errPayload); err != nil {
		t.Fatalf("decodificando error: %v", err)
	}
	if errPayload.Code != "rate_limited" {
		t.Errorf("Code = %q, esperaba rate_limited", errPayload.Code)
	}
}

func TestChat_LimiterErrorFailsOpen(t *testing.T) {
	// Si Redis no está disponible, el chat no debe dejar de funcionar (fail-open) — ver
	// comentario en chat.go sobre por qué un problema de infraestructura secundaria no debe
	// tumbar el chat entero.
	svc, broad := newTestService(&fakeLimiter{allow: false, err: errBoom}, nil)

	svc.HandleSendChat("ash", protocol.SendChatPayload{Channel: "global", Message: "hola"})

	if len(broad.sent) != 1 || broad.sent[0].kind != "global" {
		t.Errorf("con error del limiter, mensajes = %+v, esperaba que el mensaje pasara igual (fail-open)", broad.sent)
	}
}
