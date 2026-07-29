package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"pokemon-online/server/internal/auth"
	"pokemon-online/server/internal/battle"
	"pokemon-online/server/internal/battlesession"
	"pokemon-online/server/internal/character"
	"pokemon-online/server/internal/chat"
	"pokemon-online/server/internal/config"
	"pokemon-online/server/internal/db"
	"pokemon-online/server/internal/email"
	"pokemon-online/server/internal/inventory"
	"pokemon-online/server/internal/market"
	"pokemon-online/server/internal/pokemon"
	"pokemon-online/server/internal/protocol"
	"pokemon-online/server/internal/ratelimit"
	"pokemon-online/server/internal/social"
	"pokemon-online/server/internal/trade"
	"pokemon-online/server/internal/wildencounter"
	"pokemon-online/server/internal/world"
	"pokemon-online/server/internal/ws"
)

// maxChatMessagesPerWindow es el límite de mensajes de chat por jugador dentro
// de chatRateWindow, aplicado vía Redis (ver internal/ratelimit).
const (
	maxChatMessagesPerWindow = 5
	chatRateWindow           = 1 * time.Second
)

// tradeTimeout es cuánto puede vivir una sesión de trade sin completarse antes de
// cancelarse sola y liberar el Pokémon bloqueado (ver Router.SweepExpiredTrades).
const tradeTimeout = 2 * time.Minute

func main() {
	cfg := config.Load()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.ParseLogLevel()})))

	// Catálogo completo de especies/movimientos (ver server/cmd/gendata) — tiene que cargarse
	// ANTES de aceptar cualquier conexión: sin esto, pokemon.SpeciesTypes/battle.MoveByID
	// devuelven nada y toda batalla/creación de inicial fallaría en silencio.
	speciesPath := cfg.DataDir + "/species.json"
	if err := pokemon.LoadSpeciesCatalog(speciesPath); err != nil {
		slog.Error("no se pudo cargar el catálogo de especies", "path", speciesPath, "error", err)
		os.Exit(1)
	}
	movesPath := cfg.DataDir + "/moves.json"
	if err := battle.LoadMoveCatalog(movesPath); err != nil {
		slog.Error("no se pudo cargar el catálogo de movimientos", "path", movesPath, "error", err)
		os.Exit(1)
	}
	encountersPath, learnsetsPath := cfg.DataDir+"/encounters.json", cfg.DataDir+"/learnsets.json"
	if err := wildencounter.LoadCatalogs(encountersPath, learnsetsPath); err != nil {
		slog.Error("no se pudo cargar el catálogo de encuentros/learnsets", "error", err)
		os.Exit(1)
	}

	database, err := db.New(cfg.DatabaseURL)
	if err != nil {
		slog.Error("no se pudo conectar a la base de datos", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 3*time.Second)
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		slog.Warn("no se pudo conectar a redis, el rate limiting de chat fallará abierto", "addr", cfg.RedisAddr, "error", err)
	}
	cancelPing()
	chatLimiter := ratelimit.NewChatLimiter(rdb, maxChatMessagesPerWindow, chatRateWindow)

	hub := ws.NewHub()
	var emailSender email.Sender = email.ConsoleSender{}
	if cfg.SMTPHost != "" {
		emailSender = email.SMTPSender{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.SMTPFrom,
		}
		slog.Info("verificación de email: usando SMTP real", "component", "email", "host", cfg.SMTPHost)
	} else {
		slog.Info("verificación de email: SMTP_HOST no configurado, los correos solo se loguean (no se mandan de verdad)", "component", "email")
	}
	authSvc := auth.NewService(database, cfg.JWTSecret, emailSender, cfg.PublicURL)
	tradeSvc := trade.NewService(database)
	friendsSvc := social.NewService(database)
	partySvc := social.NewPartyService(database)
	marketSvc := market.NewService(database)
	guildSvc := social.NewGuildService(database)
	characterSvc := character.NewService(database)
	pokemonSvc := pokemon.NewService(database)
	inventorySvc := inventory.NewService(database)
	battleSvc := battlesession.NewService(pokemonSvc, inventorySvc)
	wildSvc := wildencounter.NewService(pokemonSvc, inventorySvc)
	lookup := world.NewHubLookup(hub)
	guildLookup := world.NewGuildLookup(guildSvc)
	chatSvc := chat.NewService(lookup, hub, chatLimiter, guildLookup)
	router := world.NewRouter(hub, chatSvc, tradeSvc, friendsSvc, partySvc, marketSvc, guildSvc, characterSvc, battleSvc, inventorySvc, wildSvc, pokemonSvc)

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			router.SweepExpiredTrades(tradeTimeout)
		}
	}()

	authenticate := func(p protocol.LoginPayload) (ws.AuthResult, error) {
		var result auth.AuthResult
		var err error
		if p.SessionToken != "" {
			result, err = authSvc.LoginWithToken(p.SessionToken)
		} else {
			result, err = authSvc.Login(p.Username, p.Password)
		}
		if err != nil {
			return ws.AuthResult{}, err
		}

		token, err := authSvc.IssueToken(result.AccountID, result.CharacterID)
		if err != nil {
			return ws.AuthResult{}, err
		}

		return ws.AuthResult{
			AccountID: result.AccountID, CharacterID: result.CharacterID,
			Nickname: result.Nickname, SpriteID: "default",
			MapID: result.MapID, X: result.PosX, Y: result.PosY,
			SessionToken: token, Color: result.Color,
			Money: result.Money, StarterSpecies: result.StarterSpecies,
		}, nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/register", handleRegister(authSvc, pokemonSvc, inventorySvc))
	mux.HandleFunc("/ws", ws.ServeWS(hub, router, authenticate))
	mux.HandleFunc("/verify-email", handleVerifyEmail(authSvc))
	mux.HandleFunc("/request-password-reset", handleRequestPasswordReset(authSvc))
	mux.HandleFunc("/reset-password", handleResetPassword(authSvc))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/client-version", handleClientVersion(cfg))
	mux.HandleFunc("/client-download", handleClientDownload(cfg))
	mux.HandleFunc("/server-status", handleServerStatus(hub, cfg))
	mux.HandleFunc("/news", handleNews(cfg))

	slog.Info("servidor escuchando", "port", cfg.HTTPPort, "ws_path", "/ws")
	if err := http.ListenAndServe(":"+cfg.HTTPPort, mux); err != nil {
		slog.Error("error en servidor http", "error", err)
		os.Exit(1)
	}
}

type registerRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	RomID    string `json:"rom_id"`
	Nickname string `json:"nickname"`
	// StarterSpecies es opcional (estilo PokeMMO: elegir el inicial al crear personaje, ver
	// pokemon.StarterCatalog para los válidos). Si se omite o no es uno de los 3 iniciales
	// válidos, el personaje queda sin equipo — puede elegirlo después por otra vía.
	StarterSpecies int `json:"starter_species"`
}

// startingItemKit es lo que recibe una cuenta nueva para que el Bag no sea un menú vacío
// permanente (todavía no hay tienda ni forma de conseguir objetos jugando) — cantidades
// modestas, pensadas para "activar y probar con amigos", no para un balance de juego real.
var startingItemKit = map[int]int{
	inventory.ItemPotion:   3,
	inventory.ItemRevive:   1,
	inventory.ItemPokeBall: 5,
	inventory.ItemOldRod:   1, // para poder probar encuentros de pesca de entrada, ver internal/wildencounter
}

func handleRegister(authSvc *auth.Service, pokemonSvc *pokemon.Service, inventorySvc *inventory.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req registerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		result, err := authSvc.Register(req.Username, req.Email, req.Password, req.RomID, req.Nickname)
		if err != nil {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		if req.StarterSpecies != 0 {
			if starter, err := pokemonSvc.AddStarter(result.CharacterID, req.StarterSpecies); err != nil {
				slog.Warn("no se pudo crear el inicial elegido", "component", "register", "error", err)
			} else {
				result.StarterSpecies = starter.Species
			}
		}

		for itemID, qty := range startingItemKit {
			if err := inventorySvc.Grant(result.CharacterID, itemID, qty); err != nil {
				slog.Warn("no se pudo dar el kit inicial de objetos", "component", "register", "item_id", itemID, "error", err)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}
}

// handleVerifyEmail atiende el link mandado por correo en el registro (ver
// auth.Service.Register) — GET simple con el token en la query string, para que un click desde
// el cliente de correo alcance (nada de JS/formulario). Página de texto plano, no HTML: no vale
// la pena una plantilla real todavía para una sola línea de confirmación.
func handleVerifyEmail(authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Falta el token de verificación."))
			return
		}
		if err := authSvc.VerifyEmail(token); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("El link de verificación es inválido o ya se usó."))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("¡Cuenta verificada! Ya podés volver al juego."))
	}
}

type requestPasswordResetBody struct {
	Email string `json:"email"`
}

// handleRequestPasswordReset SIEMPRE responde 200 con el mismo mensaje genérico, exista o no
// una cuenta con ese email (ver auth.Service.RequestPasswordReset) — filtrar por "cuenta no
// encontrada" acá le daría a cualquiera una forma de probar emails al azar y ver cuáles están
// registrados.
func handleRequestPasswordReset(authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req requestPasswordResetBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := authSvc.RequestPasswordReset(req.Email); err != nil {
			slog.Error("error pidiendo recuperación de contraseña", "component", "auth", "error", err)
			// Fallo real de infraestructura (DB caída) — igual no distinguimos el mensaje al
			// cliente para no filtrar nada, pero sí devolvemos 500 para que el cliente sepa
			// reintentar en vez de asumir que el correo ya va en camino.
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"message": "Si esa dirección está registrada, te mandamos un correo con instrucciones.",
		})
	}
}

type resetPasswordBody struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// handleResetPassword consume el token del link (ver RequestPasswordReset) y fija la
// contraseña nueva.
func handleResetPassword(authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req resetPasswordBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" || req.NewPassword == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := authSvc.ResetPassword(req.Token, req.NewPassword); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Contraseña actualizada. Ya podés iniciar sesión con la nueva."})
	}
}

// clientVersionResponse es lo que el Launcher (client-engine/Launcher) espera para decidir si
// tiene que descargar un bundle nuevo — ver ese proyecto para el consumidor real.
type clientVersionResponse struct {
	Version     string `json:"version"`
	DownloadURL string `json:"download_url"`
}

// handleClientVersion le dice al Launcher qué versión de cliente es "la última" y de dónde
// descargarla — cfg.ClientVersion sale del archivo VERSION en la raíz del repo (ver
// config.readVersionFile), así que no hay nada que mantener sincronizado a mano acá.
func handleClientVersion(cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(clientVersionResponse{
			Version:     cfg.ClientVersion,
			DownloadURL: cfg.PublicURL + "/client-download",
		})
	}
}

// handleClientDownload sirve el .zip armado por scripts/build-client-bundle.ps1 tal cual —
// nada de streaming/rangos especiales, el bundle es chico (cliente + memory-maps + data/pokemon
// + sprites, nunca la ROM) y esto corre para un puñado de amigos, no una CDN pública.
func handleClientDownload(cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(cfg.ClientBundlePath); err != nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Todavía no se publicó ningún bundle de cliente (ver scripts/build-client-bundle.ps1)."))
			return
		}
		w.Header().Set("Content-Disposition", "attachment; filename=\"client-bundle.zip\"")
		http.ServeFile(w, r, cfg.ClientBundlePath)
	}
}

type serverStatusResponse struct {
	Status        string `json:"status"`
	PlayersOnline int    `json:"players_online"`
	Version       string `json:"version"`
}

// handleServerStatus le da al Launcher lo que necesita para mostrar "estado del servidor /
// jugadores conectados" en la pantalla principal — hub.Count() ya lleva ese número en memoria
// (un jugador por entrada mientras el WebSocket sigue abierto), no hace falta ninguna consulta
// a la base de datos.
func handleServerStatus(hub *ws.Hub, cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(serverStatusResponse{
			Status:        "online",
			PlayersOnline: hub.Count(),
			Version:       cfg.ClientVersion,
		})
	}
}

// handleNews sirve tal cual el contenido de cfg.NewsPath (un JSON de texto plano que quien
// hostee el server edita a mano, mismo criterio que launcher-config.json) — sin noticias
// configuradas, un array vacío es una respuesta válida (el Launcher simplemente no muestra
// nada, no es un error).
func handleNews(cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data, err := os.ReadFile(cfg.NewsPath)
		if err != nil {
			_, _ = w.Write([]byte("[]"))
			return
		}
		_, _ = w.Write(data)
	}
}
