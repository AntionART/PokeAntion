package config

import (
	"log/slog"
	"os"
	"strings"
)

// Config centraliza toda la configuración leída de variables de entorno.
// Mantenerlo simple: nada de librerías externas de config todavía, no hace falta.
type Config struct {
	HTTPPort    string
	DatabaseURL string
	RedisAddr   string
	JWTSecret   string
	LogLevel    string // debug | info | warn | error
}

func Load() Config {
	return Config{
		HTTPPort:    getEnv("HTTP_PORT", "8080"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://pokemon:pokemon@localhost:5432/pokemon_online?sslmode=disable"),
		RedisAddr:   getEnv("REDIS_ADDR", "localhost:6379"),
		JWTSecret:   getEnv("JWT_SECRET", "dev-secret-change-me"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ParseLogLevel traduce LOG_LEVEL a slog.Level; por defecto info si el valor no se reconoce.
func (c Config) ParseLogLevel() slog.Level {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
