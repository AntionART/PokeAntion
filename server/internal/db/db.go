package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// New abre el pool de conexiones a Postgres. Se llama una vez al arrancar el servidor
// y el *sql.DB resultante se pasa (inyecta) a cada módulo que lo necesite (auth, world, trade...).
func New(databaseURL string) (*sql.DB, error) {
	conn, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("abriendo conexión a postgres: %w", err)
	}

	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(25)
	conn.SetConnMaxLifetime(5 * time.Minute)

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("no se pudo hacer ping a postgres: %w", err)
	}

	return conn, nil
}
