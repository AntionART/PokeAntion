package ws

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"pokemon-online/server/internal/protocol"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// En producción: restringir CheckOrigin a los dominios reales del cliente.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Connection envuelve la conexión websocket cruda y le da forward/backward
// hacia el Hub y hacia el Router de mensajes.
type Connection struct {
	ws *websocket.Conn
}

// Router es la interfaz que implementa el paquete de más alto nivel (server principal)
// para procesar cada mensaje entrante ya autenticado. Se inyecta para no acoplar
// ws con auth/chat/trade directamente.
type Router interface {
	HandleMessage(characterID string, env protocol.Envelope)
	// HandleBinaryMessage recibe frames binarios crudos (hoy: paquetes de audio del chat de
	// voz, PCM16 mono). Separado de HandleMessage porque no son JSON — mezclar los dos
	// formatos en un solo tipo de mensaje obligaría a base64-encodear el audio, mucho más
	// pesado que mandarlo como frame binario nativo de WebSocket.
	HandleBinaryMessage(characterID string, data []byte)
	HandleConnect(characterID string)
	HandleDisconnect(characterID string)
}

// AuthResult es lo que devuelve la función de autenticación inyectada desde main.go
// tras validar un login (por usuario/contraseña o por session_token JWT).
type AuthResult struct {
	AccountID    string
	CharacterID  string
	Nickname     string
	SpriteID     string
	MapID        string
	X, Y         int
	SessionToken string
	Color        string
}

// ServeWS hace el upgrade HTTP->WebSocket, autentica la primera petición (`login`)
// y luego mantiene el loop de lectura/escritura hasta que se cierra la conexión.
func ServeWS(hub *Hub, router Router, authenticate func(protocol.LoginPayload) (AuthResult, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Error("error en upgrade de websocket", "component", "ws", "error", err)
			return
		}
		// Cierra el socket TCP subyacente cuando este handler retorna, sin importar
		// el camino de salida. Antes de este fix nada llamaba a conn.Close() tras un
		// cierre limpio (readPump/writePump solo dejaban de leer/escribir), dejando
		// la conexión a medio cerrar indefinidamente del lado del servidor.
		defer conn.Close()

		// --- Paso 1: esperar el mensaje de login antes de aceptar nada más ---
		var loginEnv protocol.Envelope
		if err := conn.ReadJSON(&loginEnv); err != nil || loginEnv.Type != "login" {
			_ = conn.WriteJSON(protocol.Envelope{Type: "login_error"})
			return
		}

		var loginPayload protocol.LoginPayload
		if err := json.Unmarshal(loginEnv.Payload, &loginPayload); err != nil {
			return
		}

		result, err := authenticate(loginPayload)
		if err != nil {
			errEnv, _ := protocol.NewEnvelope("login_error", protocol.ErrorPayload{
				Code: "invalid_credentials", Message: "usuario o contraseña incorrectos",
			})
			_ = conn.WriteJSON(errEnv)
			return
		}

		client := &Client{
			CharacterID: result.CharacterID, Nickname: result.Nickname, SpriteID: result.SpriteID,
			MapID: result.MapID, X: result.X, Y: result.Y, Facing: "down", State: "idle",
			Color: result.Color,
			send:  make(chan protocol.Envelope, 32),
			// Buffer más chico que el de JSON: la voz es sensible a latencia (si se acumula
			// atraso, mejor descartar paquetes viejos que reproducirlos tarde) y de alto
			// volumen (varios paquetes por segundo mientras se habla).
			sendBinary: make(chan []byte, 8),
			conn:       &Connection{ws: conn},
		}
		hub.Register(client)
		router.HandleConnect(client.CharacterID)

		okEnv, _ := protocol.NewEnvelope("login_ok", protocol.LoginOKPayload{
			AccountID: result.AccountID, CharacterID: result.CharacterID, SessionToken: result.SessionToken,
			MapID: result.MapID, PosX: result.X, PosY: result.Y, Color: result.Color,
		})
		_ = conn.WriteJSON(okEnv)

		// --- Paso 2: loops de lectura y escritura ---
		go writePump(client)
		readPump(client, hub, router)
	}
}

func readPump(c *Client, hub *Hub, router Router) {
	// Orden importa: HandleDisconnect necesita que el jugador siga en el Hub (para poder
	// avisarle a su propio mapa "player_left_map" con el mapID correcto) antes de que
	// Unregister lo saque. Los defer corren en orden LIFO, así que Unregister se declara
	// primero para ejecutarse último.
	defer hub.Unregister(c.CharacterID)
	defer router.HandleDisconnect(c.CharacterID)

	// 8192 en vez de los 4096 originales: suficiente para JSON normal y para paquetes
	// cortos de voz (PCM16 mono, ~100ms a 16kHz = 3200 bytes) sin acercarse al límite.
	c.conn.ws.SetReadLimit(8192)
	for {
		messageType, data, err := c.conn.ws.ReadMessage()
		if err != nil {
			return // desconexión -> cerrar sesión
		}
		switch messageType {
		case websocket.TextMessage:
			var env protocol.Envelope
			if err := json.Unmarshal(data, &env); err != nil {
				continue // mensaje de texto malformado: se ignora, no se tira la sesión entera
			}
			router.HandleMessage(c.CharacterID, env)
		case websocket.BinaryMessage:
			router.HandleBinaryMessage(c.CharacterID, data)
		}
	}
}

func writePump(c *Client) {
	ticker := time.NewTicker(30 * time.Second) // ping de keep-alive
	defer ticker.Stop()

	for {
		select {
		case env, ok := <-c.send:
			if !ok {
				_ = c.conn.ws.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.ws.WriteJSON(env); err != nil {
				return
			}
		case data, ok := <-c.sendBinary:
			if !ok {
				_ = c.conn.ws.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.conn.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
