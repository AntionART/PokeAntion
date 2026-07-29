package ws

import (
	"testing"

	"pokemon-online/server/internal/protocol"
)

// Tests unitarios puros (sin Postgres/red real): Hub es estado en memoria, así que se puede
// construir un *Client a mano (mismo paquete, campos no exportados accesibles) sin pasar por
// una conexión websocket real. connection.go/HandleConnect no se ejercitan acá — son plomería
// fina ya cubierta indirectamente por los smoke tests (scripts/*.js) que sí abren sockets
// reales; lo que faltaba cubrir con un test rápido y determinístico era la lógica del Hub en
// sí (límite de velocidad, agrupamiento por mapa, broadcast con exclusión).

func newTestClient(characterID, mapID string, x, y int) *Client {
	return &Client{
		CharacterID: characterID, Nickname: characterID, MapID: mapID, X: x, Y: y,
		Facing: "down", State: "idle", Color: "default",
		send:       make(chan protocol.Envelope, 8),
		sendBinary: make(chan []byte, 8),
	}
}

func TestHub_RegisterAndUnregister(t *testing.T) {
	h := NewHub()
	c := newTestClient("ash", "MAP_ROUTE101", 5, 5)
	h.Register(c)

	if mapID := h.MapOfCharacter("ash"); mapID != "MAP_ROUTE101" {
		t.Errorf("MapOfCharacter = %q, esperaba MAP_ROUTE101", mapID)
	}
	players := h.PlayersInMap("MAP_ROUTE101")
	if len(players) != 1 || players[0].CharacterID != "ash" {
		t.Errorf("PlayersInMap = %+v, esperaba solo a ash", players)
	}

	h.Unregister("ash")
	if mapID := h.MapOfCharacter("ash"); mapID != "" {
		t.Errorf("MapOfCharacter tras Unregister = %q, esperaba vacío", mapID)
	}
	if players := h.PlayersInMap("MAP_ROUTE101"); len(players) != 0 {
		t.Errorf("PlayersInMap tras Unregister = %+v, esperaba vacío", players)
	}
}

// TestHub_Count cubre Hub.Count() (agregado para /server-status, ver main.go
// handleServerStatus) — jugadores conectados debe reflejar Register/Unregister en tiempo real,
// sin importar en qué mapa esté cada uno.
func TestHub_Count(t *testing.T) {
	h := NewHub()
	if got := h.Count(); got != 0 {
		t.Fatalf("Count() en un hub vacío = %d, esperaba 0", got)
	}

	h.Register(newTestClient("ash", "MAP_ROUTE101", 5, 5))
	h.Register(newTestClient("misty", "MAP_LITTLEROOT_TOWN", 10, 12))
	if got := h.Count(); got != 2 {
		t.Fatalf("Count() tras 2 registros = %d, esperaba 2", got)
	}

	h.Unregister("ash")
	if got := h.Count(); got != 1 {
		t.Fatalf("Count() tras un Unregister = %d, esperaba 1", got)
	}

	h.Unregister("misty")
	if got := h.Count(); got != 0 {
		t.Fatalf("Count() tras desconectar a todos = %d, esperaba 0", got)
	}
}

func TestHub_UpdatePosition_AcceptsNormalMove(t *testing.T) {
	h := NewHub()
	h.Register(newTestClient("ash", "MAP_ROUTE101", 5, 5))

	// Primer movimiento tras conectarse: LastMoveAt está en cero, así que el chequeo de
	// velocidad se salta siempre (ver comentario en UpdatePosition) — no importa la distancia.
	_, _, accepted, cx, cy := h.UpdatePosition("ash", "MAP_ROUTE101", 100, 100, "down", "walking")
	if !accepted || cx != 100 || cy != 100 {
		t.Errorf("primer movimiento = accepted=%v (%d,%d), esperaba aceptado en (100,100)", accepted, cx, cy)
	}

	// Un segundo movimiento chico e inmediato después también debe aceptarse (dentro de
	// moveTolerance incluso con elapsed≈0).
	_, _, accepted2, cx2, cy2 := h.UpdatePosition("ash", "MAP_ROUTE101", 101, 100, "right", "walking")
	if !accepted2 || cx2 != 101 || cy2 != 100 {
		t.Errorf("segundo movimiento chico = accepted=%v (%d,%d), esperaba aceptado", accepted2, cx2, cy2)
	}
}

func TestHub_UpdatePosition_RejectsTeleport(t *testing.T) {
	h := NewHub()
	h.Register(newTestClient("ash", "MAP_ROUTE101", 5, 5))
	h.UpdatePosition("ash", "MAP_ROUTE101", 5, 5, "down", "idle") // ancla LastMoveAt a "ahora"

	// 200 tiles instantáneos en el mismo mapa: muy por encima de maxTilesPerSecond incluso
	// con jitter — debe rechazarse y devolver la posición corregida (la última válida).
	_, _, accepted, cx, cy := h.UpdatePosition("ash", "MAP_ROUTE101", 205, 5, "right", "running")
	if accepted {
		t.Fatalf("teletransporte de 200 tiles fue aceptado, no debería")
	}
	if cx != 5 || cy != 5 {
		t.Errorf("posición corregida = (%d,%d), esperaba (5,5) (la última válida)", cx, cy)
	}

	// Tras el rechazo, el jugador debe poder seguir moviéndose normalmente (no queda "trabado").
	_, _, accepted2, _, _ := h.UpdatePosition("ash", "MAP_ROUTE101", 6, 5, "right", "walking")
	if !accepted2 {
		t.Errorf("movimiento normal tras un rechazo = %v, esperaba aceptado", accepted2)
	}
}

func TestHub_UpdatePosition_MapChangeBypassesSpeedCheck(t *testing.T) {
	h := NewHub()
	h.Register(newTestClient("ash", "MAP_ROUTE101", 5, 5))
	h.UpdatePosition("ash", "MAP_ROUTE101", 5, 5, "down", "idle")

	// Cambiar de mapa con coordenadas completamente distintas (de otro espacio, no
	// comparables) no debe activar el rechazo de velocidad.
	oldMap, newMap, accepted, _, _ := h.UpdatePosition("ash", "MAP_LITTLEROOT_TOWN", 300, 300, "down", "walking")
	if !accepted {
		t.Fatalf("cambio de mapa fue rechazado, no debería activar el chequeo de velocidad")
	}
	if oldMap != "MAP_ROUTE101" || newMap != "MAP_LITTLEROOT_TOWN" {
		t.Errorf("oldMap=%q newMap=%q, esperaba MAP_ROUTE101 -> MAP_LITTLEROOT_TOWN", oldMap, newMap)
	}

	players101 := h.PlayersInMap("MAP_ROUTE101")
	playersLittleroot := h.PlayersInMap("MAP_LITTLEROOT_TOWN")
	if len(players101) != 0 {
		t.Errorf("PlayersInMap(MAP_ROUTE101) tras cambiar de mapa = %+v, esperaba vacío", players101)
	}
	if len(playersLittleroot) != 1 {
		t.Errorf("PlayersInMap(MAP_LITTLEROOT_TOWN) = %+v, esperaba 1", playersLittleroot)
	}
}

func TestHub_UpdatePosition_UnknownCharacterRejected(t *testing.T) {
	h := NewHub()
	_, _, accepted, _, _ := h.UpdatePosition("nadie_conectado", "MAP_ROUTE101", 5, 5, "down", "idle")
	if accepted {
		t.Errorf("UpdatePosition de un personaje no registrado fue aceptado, no debería")
	}
}

func TestHub_BroadcastToMap_ExcludesSenderAndOtherMaps(t *testing.T) {
	h := NewHub()
	ash := newTestClient("ash", "MAP_ROUTE101", 0, 0)
	misty := newTestClient("misty", "MAP_ROUTE101", 1, 1)
	brock := newTestClient("brock", "MAP_LITTLEROOT_TOWN", 0, 0)
	h.Register(ash)
	h.Register(misty)
	h.Register(brock)

	env, _ := protocol.NewEnvelope("player_update", protocol.PlayerUpdatePayload{CharacterID: "ash"})
	h.BroadcastToMap("MAP_ROUTE101", env, "ash")

	select {
	case <-ash.send:
		t.Errorf("el emisor (ash) recibió su propio broadcast, debería estar excluido")
	default:
	}
	select {
	case <-misty.send:
		// esperado: misty está en el mismo mapa y no es el emisor
	default:
		t.Errorf("misty (mismo mapa, no emisora) no recibió el broadcast")
	}
	select {
	case <-brock.send:
		t.Errorf("brock (otro mapa) recibió un broadcast que no era para su mapa")
	default:
	}
}

func TestHub_SendTo_UnknownCharacterReturnsFalse(t *testing.T) {
	h := NewHub()
	env, _ := protocol.NewEnvelope("chat_message", protocol.ChatMessagePayload{})
	if ok := h.SendTo("nadie", env); ok {
		t.Errorf("SendTo a un personaje no conectado devolvió true, esperaba false")
	}
}

func TestHub_SetColorAndColorOf(t *testing.T) {
	h := NewHub()
	h.Register(newTestClient("ash", "MAP_ROUTE101", 0, 0))

	update, ok := h.SetColor("ash", "blue")
	if !ok || update.Color != "blue" {
		t.Errorf("SetColor = (%+v, %v), esperaba color=blue ok=true", update, ok)
	}
	if got := h.ColorOfCharacter("ash"); got != "blue" {
		t.Errorf("ColorOfCharacter = %q, esperaba blue", got)
	}
}

func TestHub_PositionOfCharacter(t *testing.T) {
	h := NewHub()
	h.Register(newTestClient("ash", "MAP_ROUTE101", 7, 9))

	mapID, x, y, ok := h.PositionOfCharacter("ash")
	if !ok || mapID != "MAP_ROUTE101" || x != 7 || y != 9 {
		t.Errorf("PositionOfCharacter = (%q,%d,%d,%v), esperaba (MAP_ROUTE101,7,9,true)", mapID, x, y, ok)
	}
	if _, _, _, ok := h.PositionOfCharacter("nadie"); ok {
		t.Errorf("PositionOfCharacter de un personaje no conectado devolvió ok=true")
	}
}
