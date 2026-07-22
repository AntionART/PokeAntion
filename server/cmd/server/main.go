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
	"pokemon-online/server/internal/battlesession"
	"pokemon-online/server/internal/character"
	"pokemon-online/server/internal/chat"
	"pokemon-online/server/internal/config"
	"pokemon-online/server/internal/db"
	"pokemon-online/server/internal/market"
	"pokemon-online/server/internal/pokemon"
	"pokemon-online/server/internal/protocol"
	"pokemon-online/server/internal/ratelimit"
	"pokemon-online/server/internal/social"
	"pokemon-online/server/internal/trade"
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
	authSvc := auth.NewService(database, cfg.JWTSecret)
	tradeSvc := trade.NewService(database)
	friendsSvc := social.NewService(database)
	partySvc := social.NewPartyService(database)
	marketSvc := market.NewService(database)
	guildSvc := social.NewGuildService(database)
	characterSvc := character.NewService(database)
	pokemonSvc := pokemon.NewService(database)
	battleSvc := battlesession.NewService(pokemonSvc)
	lookup := world.NewHubLookup(hub)
	guildLookup := world.NewGuildLookup(guildSvc)
	chatSvc := chat.NewService(lookup, hub, chatLimiter, guildLookup)
	router := world.NewRouter(hub, chatSvc, tradeSvc, friendsSvc, partySvc, marketSvc, guildSvc, characterSvc, battleSvc)

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
	mux.HandleFunc("/register", handleRegister(authSvc, pokemonSvc))
	mux.HandleFunc("/ws", ws.ServeWS(hub, router, authenticate))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

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

func handleRegister(authSvc *auth.Service, pokemonSvc *pokemon.Service) http.HandlerFunc {
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

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}
}
