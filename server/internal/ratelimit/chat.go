// Package ratelimit implementa límites de tasa respaldados por Redis, compartidos
// entre todas las instancias del servidor (a diferencia de un contador en memoria,
// que solo limitaría dentro de un único proceso).
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ChatLimiter limita mensajes de chat por jugador con un contador de ventana fija:
// INCR sobre una clave por jugador, con EXPIRE en el primer mensaje de cada ventana.
type ChatLimiter struct {
	rdb          *redis.Client
	maxPerWindow int64
	window       time.Duration
}

func NewChatLimiter(rdb *redis.Client, maxPerWindow int, window time.Duration) *ChatLimiter {
	return &ChatLimiter{rdb: rdb, maxPerWindow: int64(maxPerWindow), window: window}
}

// Allow devuelve false si el jugador ya alcanzó el máximo de mensajes permitido
// dentro de la ventana actual. Un error de Redis se propaga al llamador, que
// decide si falla abierto (deja pasar) o cerrado.
func (l *ChatLimiter) Allow(characterID string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	key := fmt.Sprintf("chat_rl:%s", characterID)
	count, err := l.rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if count == 1 {
		if err := l.rdb.Expire(ctx, key, l.window).Err(); err != nil {
			return false, err
		}
	}
	return count <= l.maxPerWindow, nil
}
