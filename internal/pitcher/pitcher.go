// Package pitcher delivers programme items as homerun2 messages.
package pitcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/redis/go-redis/v9"
	homerun "github.com/stuttgart-things/homerun-library/v4"
)

// Pitcher delivers one message and returns where it went.
type Pitcher interface {
	Pitch(ctx context.Context, msg homerun.Message) (objectID string, err error)
	Ready(ctx context.Context) error
	Close() error
}

// Redis pitches into a Redis stream through homerun-library: the message as
// a Redis JSON document, its ID on the stream (Stream, normally "ambient").
type Redis struct {
	Stream  string
	pitcher *homerun.Pitcher
	ping    *redis.Client
}

// NewRedis opens the connection. rc.Stream is ignored in favour of stream so
// the target stays explicit.
func NewRedis(rc homerun.RedisConfig, stream string) *Redis {
	return &Redis{
		Stream:  stream,
		pitcher: homerun.NewPitcher(rc),
		ping: redis.NewClient(&redis.Options{
			Addr:     rc.Addr + ":" + rc.Port,
			Password: rc.Password,
		}),
	}
}

func (r *Redis) Pitch(ctx context.Context, msg homerun.Message) (string, error) {
	id, _, err := r.pitcher.Enqueue(ctx, msg, r.Stream)
	if err != nil {
		return "", fmt.Errorf("pitch to stream %s: %w", r.Stream, err)
	}
	return id, nil
}

// Ready pings Redis.
func (r *Redis) Ready(ctx context.Context) error {
	return r.ping.Ping(ctx).Err()
}

func (r *Redis) Close() error {
	err := r.pitcher.Close()
	if perr := r.ping.Close(); err == nil {
		err = perr
	}
	return err
}

// Writer prints each message as a JSON line instead of pitching it (dry run).
type Writer struct {
	W  io.Writer
	mu sync.Mutex
	n  int
}

func (w *Writer) Pitch(_ context.Context, msg homerun.Message) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.n++
	data, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	if _, err := fmt.Fprintln(w.W, string(data)); err != nil {
		return "", err
	}
	return fmt.Sprintf("dry-run-%d", w.n), nil
}

func (w *Writer) Ready(context.Context) error { return nil }
func (w *Writer) Close() error                { return nil }
