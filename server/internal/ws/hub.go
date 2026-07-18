package ws

import (
	"log/slog"
	"math"
	"sync"
	"time"

	"pokemon-online/server/internal/protocol"
)

// maxTilesPerSecond acota la velocidad de movimiento aceptada por el servidor. Generoso a
// propósito (cubre caminar, correr y bicicleta, más margen para jitter de red/frames
// perdidos): el objetivo es rechazar teletransportes obvios, no microgestionar el ritmo
// exacto de animación del cliente.
const maxTilesPerSecond = 8.0

// moveTolerance se suma a la distancia máxima permitida para no rechazar el primer
// movimiento tras un intervalo muy corto por redondeo/latencia.
const moveTolerance = 1.5

// Client representa una conexión activa de un jugador.
type Client struct {
	CharacterID string
	Nickname    string
	SpriteID    string
	MapID       string
	X, Y        int
	Facing      string
	State       string
	Color       string // ver world.AllowedSpriteColors — "default" si nunca lo cambió
	LastMoveAt  time.Time // último movimiento ACEPTADO; base para el chequeo de velocidad

	send       chan protocol.Envelope
	sendBinary chan []byte // paquetes de voz salientes hacia este cliente
	conn       *Connection // wrapper sobre *websocket.Conn, ver connection.go
}

// Hub mantiene el registro de todos los clientes conectados, agrupados por mapa,
// para poder difundir "player_update" solo a quienes están en el mismo mapa
// (esto es la mitigación de escalabilidad mencionada en el diseño: nunca
// difundir a todo el servidor, solo al área de interés).
type Hub struct {
	mu      sync.RWMutex
	clients map[string]*Client            // characterID -> client
	byMap   map[string]map[string]*Client // mapID -> characterID -> client
}

func NewHub() *Hub {
	return &Hub{
		clients: make(map[string]*Client),
		byMap:   make(map[string]map[string]*Client),
	}
}

func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.clients[c.CharacterID] = c
	h.addToMapLocked(c)

	slog.Info("jugador conectado", "component", "hub", "nickname", c.Nickname, "map_id", c.MapID)
}

func (h *Hub) Unregister(characterID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	c, ok := h.clients[characterID]
	if !ok {
		return
	}
	h.removeFromMapLocked(c)
	delete(h.clients, characterID)
	close(c.send)
	close(c.sendBinary)

	slog.Info("jugador desconectado", "component", "hub", "nickname", c.Nickname)
}

func (h *Hub) addToMapLocked(c *Client) {
	if h.byMap[c.MapID] == nil {
		h.byMap[c.MapID] = make(map[string]*Client)
	}
	h.byMap[c.MapID][c.CharacterID] = c
}

func (h *Hub) removeFromMapLocked(c *Client) {
	if m, ok := h.byMap[c.MapID]; ok {
		delete(m, c.CharacterID)
		if len(m) == 0 {
			delete(h.byMap, c.MapID)
		}
	}
}

// UpdatePosition mueve al cliente (posiblemente cambiando de mapa), aplicando un límite de
// velocidad físicamente razonable: si la distancia recorrida desde el último movimiento
// aceptado excede lo posible en el tiempo transcurrido, se rechaza — no se toca el estado
// del cliente — y accepted vuelve false, con correctedX/correctedY apuntando a la última
// posición válida para que el llamador se la reenvíe al cliente como corrección.
// El chequeo de velocidad se salta al cambiar de mapa (coordenadas de otro espacio, no
// comparables) y en el primer movimiento tras conectarse (LastMoveAt en cero).
func (h *Hub) UpdatePosition(characterID string, mapID string, x, y int, facing, state string) (oldMap, newMap string, accepted bool, correctedX, correctedY int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	c, exists := h.clients[characterID]
	if !exists {
		return "", "", false, 0, 0
	}

	now := time.Now()
	if mapID == c.MapID && !c.LastMoveAt.IsZero() {
		elapsed := now.Sub(c.LastMoveAt).Seconds()
		dist := math.Hypot(float64(x-c.X), float64(y-c.Y))
		if maxDist := maxTilesPerSecond*elapsed + moveTolerance; dist > maxDist {
			return c.MapID, c.MapID, false, c.X, c.Y
		}
	}

	oldMap = c.MapID
	if mapID != c.MapID {
		h.removeFromMapLocked(c)
		c.MapID = mapID
		h.addToMapLocked(c)
	}
	c.X, c.Y, c.Facing, c.State = x, y, facing, state
	c.LastMoveAt = now

	return oldMap, mapID, true, x, y
}

// BroadcastToMap envía un envelope a todos los clientes del mapa dado,
// opcionalmente excluyendo a uno (normalmente el emisor del evento).
func (h *Hub) BroadcastToMap(mapID string, env protocol.Envelope, excludeCharacterID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for id, c := range h.byMap[mapID] {
		if id == excludeCharacterID {
			continue
		}
		select {
		case c.send <- env:
		default:
			slog.Warn("buffer de envío lleno, descartando mensaje", "component", "hub", "nickname", c.Nickname)
		}
	}
}

// BroadcastBinaryToMap envía un paquete binario crudo (voz) a todos los clientes del mapa
// dado, excluyendo al emisor. Igual que BroadcastToMap pero por el canal binario, sin pasar
// por JSON — mandar audio como JSON obligaría a base64, ~33% más pesado por paquete.
func (h *Hub) BroadcastBinaryToMap(mapID string, data []byte, excludeCharacterID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for id, c := range h.byMap[mapID] {
		if id == excludeCharacterID {
			continue
		}
		select {
		case c.sendBinary <- data:
		default:
			// buffer lleno: se descarta este paquete de voz sin loguear (pasa seguido bajo
			// carga normal de audio; sería demasiado ruido para el log en nivel info/warn).
		}
	}
}

// BroadcastGlobal envía un envelope a TODOS los jugadores conectados, sin importar
// el mapa. Usar con moderación (chat global, anuncios) — no para movimiento.
func (h *Hub) BroadcastGlobal(env protocol.Envelope, excludeCharacterID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for id, c := range h.clients {
		if id == excludeCharacterID {
			continue
		}
		select {
		case c.send <- env:
		default:
			slog.Warn("buffer de envío lleno, descartando mensaje global", "component", "hub", "nickname", c.Nickname)
		}
	}
}

// SendTo envía un envelope a un jugador específico, si está conectado.
func (h *Hub) SendTo(characterID string, env protocol.Envelope) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	c, ok := h.clients[characterID]
	if !ok {
		return false
	}
	select {
	case c.send <- env:
		return true
	default:
		return false
	}
}

// MapOfCharacter devuelve el mapa actual de un jugador conectado, o "" si no existe.
func (h *Hub) MapOfCharacter(characterID string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if c, ok := h.clients[characterID]; ok {
		return c.MapID
	}
	return ""
}

// NicknameOfCharacter devuelve el nickname de un jugador conectado, o "" si no existe.
func (h *Hub) NicknameOfCharacter(characterID string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if c, ok := h.clients[characterID]; ok {
		return c.Nickname
	}
	return ""
}

// ColorOfCharacter devuelve el color de sprite EN MEMORIA de un jugador conectado (no pega a
// la base) — "" si no existe, para que el llamador decida el fallback.
func (h *Hub) ColorOfCharacter(characterID string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if c, ok := h.clients[characterID]; ok {
		return c.Color
	}
	return ""
}

// SetColor actualiza el color de sprite en memoria de un jugador conectado (la persistencia en
// base la maneja el llamador aparte, ver Router.handleSetColor) y devuelve su snapshot
// completo para que el llamador pueda armar un player_update sin otra consulta al Hub.
func (h *Hub) SetColor(characterID, color string) (protocol.PlayerUpdatePayload, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.clients[characterID]
	if !ok {
		return protocol.PlayerUpdatePayload{}, false
	}
	c.Color = color
	return protocol.PlayerUpdatePayload{
		CharacterID: c.CharacterID, Nickname: c.Nickname, SpriteID: c.SpriteID,
		MapID: c.MapID, X: c.X, Y: c.Y, Facing: c.Facing, State: c.State, Color: c.Color,
	}, true
}

// PositionOfCharacter devuelve mapa/x/y de un jugador conectado; ok=false si no existe.
func (h *Hub) PositionOfCharacter(characterID string) (mapID string, x, y int, ok bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if c, exists := h.clients[characterID]; exists {
		return c.MapID, c.X, c.Y, true
	}
	return "", 0, 0, false
}

// PlayersInMap devuelve un snapshot de los jugadores presentes en un mapa,
// usado para poblar el estado inicial cuando alguien entra.
func (h *Hub) PlayersInMap(mapID string) []protocol.PlayerUpdatePayload {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var out []protocol.PlayerUpdatePayload
	for _, c := range h.byMap[mapID] {
		out = append(out, protocol.PlayerUpdatePayload{
			CharacterID: c.CharacterID, Nickname: c.Nickname, SpriteID: c.SpriteID,
			MapID: c.MapID, X: c.X, Y: c.Y, Facing: c.Facing, State: c.State, Color: c.Color,
		})
	}
	return out
}
