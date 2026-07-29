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
	// DataDir apunta a data/pokemon/ (species.json + moves.json, generados por
	// server/cmd/gendata desde el código fuente real de pokeemerald) — default relativo
	// asumiendo que server.exe corre con cwd=server/ (confirmado: así es como corre hoy),
	// igual criterio que el resto de rutas relativas del proyecto (ver client-engine).
	DataDir string
	// SMTP*: credenciales para mandar el correo real de verificación de cuenta (ver
	// internal/email). Compatibles con Gmail (smtp.gmail.com:587 + contraseña de aplicación,
	// no la contraseña real de la cuenta — ver https://myaccount.google.com/apppasswords) pero
	// no específicas de Gmail, cualquier SMTP con auth PLAIN + STARTTLS sirve. Si SMTPHost queda
	// vacío (default), el servidor usa email.ConsoleSender: el registro sigue funcionando
	// igual, el link de verificación solo queda logueado en vez de mandado de verdad — nunca un
	// requisito duro para levantar el servidor.
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	// PublicURL es la base para armar el link de verificación (ej. "http://localhost:8080") —
	// separado de HTTPPort porque en un despliegue real detrás de un proxy/dominio no coinciden.
	PublicURL string
	// ClientVersion es la versión del bundle de cliente que este servidor considera "la
	// última" — el Launcher la compara contra su instalación local para decidir si
	// descargar de nuevo (ver /client-version, handleClientVersion en main.go). Por defecto
	// se lee del archivo VERSION en la raíz del repo (mismo archivo que
	// scripts/build-client-bundle.ps1 usa para nombrar el .zip que genera) — evita mantener
	// el número en dos lugares. CLIENT_VERSION la pisa si hace falta un valor distinto sin
	// tocar el archivo.
	ClientVersion string
	// ClientBundlePath es la ruta al .zip que /client-download sirve tal cual (ver
	// scripts/build-client-bundle.ps1) — no se genera en el arranque del servidor, hay que
	// correr ese script y dejar el .zip en esta ruta antes de anunciar una versión nueva.
	ClientBundlePath string
	// NewsPath apunta a un JSON de texto plano con noticias/eventos (ver /news,
	// handleNews en main.go) — mismo criterio "plantilla editable a mano" que
	// launcher-config.json: quien hostee el server edita ese archivo, no hace falta
	// ninguna base de datos ni panel de administración para esto.
	NewsPath string
}

func Load() Config {
	return Config{
		HTTPPort:     getEnv("HTTP_PORT", "8080"),
		DatabaseURL:  getEnv("DATABASE_URL", "postgres://pokemon:pokemon@localhost:5432/pokemon_online?sslmode=disable"),
		RedisAddr:    getEnv("REDIS_ADDR", "localhost:6379"),
		JWTSecret:    getEnv("JWT_SECRET", "dev-secret-change-me"),
		LogLevel:     getEnv("LOG_LEVEL", "info"),
		DataDir:      getEnv("DATA_DIR", "../data/pokemon"),
		SMTPHost:     getEnv("SMTP_HOST", ""),
		SMTPPort:     getEnv("SMTP_PORT", "587"),
		SMTPUsername: getEnv("SMTP_USERNAME", ""),
		SMTPPassword: getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:     getEnv("SMTP_FROM", ""),
		PublicURL:        getEnv("PUBLIC_URL", "http://localhost:8080"),
		ClientVersion:    getEnv("CLIENT_VERSION", readVersionFile()),
		ClientBundlePath: getEnv("CLIENT_BUNDLE_PATH", "../client-bundle.zip"),
		NewsPath:         getEnv("NEWS_PATH", "../data/news.json"),
	}
}

// readVersionFile lee el VERSION de texto plano en la raíz del repo (misma ruta relativa que
// DataDir asume, cwd=server/). Si no existe (ej. alguien corrió el servidor sin el repo
// completo al lado), "0.0.0" deja el mecanismo de update andando igual — el Launcher lo vería
// como "hay una versión más nueva" y descargaría, que es el fallo seguro correcto acá (mejor
// updatear de más que dejar a alguien atascado en un cliente viejo).
func readVersionFile() string {
	data, err := os.ReadFile("../VERSION")
	if err != nil {
		return "0.0.0"
	}
	return strings.TrimSpace(string(data))
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
