package pitcher

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	homerun "github.com/stuttgart-things/homerun-library/v4"
)

func TestWriter(t *testing.T) {
	var buf bytes.Buffer
	w := &Writer{W: &buf}
	id, err := w.Pitch(context.Background(), homerun.Message{Title: "It's 15:00", System: "radio"})
	if err != nil || id != "dry-run-1" {
		t.Fatalf("%q %v", id, err)
	}
	var got homerun.Message
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil || got.Title != "It's 15:00" {
		t.Fatalf("%q %v", buf.String(), err)
	}
}

// TestRedisIntegration pitches into a real redis-stack. It runs in the Dagger
// pipeline (`dagger call test`), which binds redis-stack and sets REDIS_ADDR;
// without REDIS_ADDR it is skipped.
func TestRedisIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set; run through `dagger call -m dagger test --src .`")
	}
	port := os.Getenv("REDIS_PORT")
	if port == "" {
		port = "6379"
	}
	rc := homerun.RedisConfig{Addr: addr, Port: port, Password: os.Getenv("REDIS_PASSWORD")}
	stream := "ambient-test-" + time.Now().Format("150405.000")

	p := NewRedis(rc, stream)
	defer func() { _ = p.Close() }()

	ctx := context.Background()
	if err := p.Ready(ctx); err != nil {
		t.Fatalf("ready: %v", err)
	}
	msg := homerun.Message{
		Title:     "312 pods on dev",
		Message:   "dev: 312 pods, 298 of them running",
		Severity:  "INFO",
		System:    "radio",
		Author:    "radio",
		Tags:      "segment=infra-facts",
		Timestamp: time.Now().Format(time.RFC3339),
	}
	id, err := p.Pitch(ctx, msg)
	if err != nil {
		t.Fatalf("pitch: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: addr + ":" + port, Password: rc.Password})
	defer func() { _ = rdb.Close() }()

	entries, err := rdb.XRange(ctx, stream, "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange: %v", err)
	}
	if len(entries) != 1 || entries[0].Values["messageID"] != id {
		t.Fatalf("stream %s = %+v, want messageID %s", stream, entries, id)
	}
	if !strings.HasSuffix(id, "-radio") {
		t.Errorf("object id %q does not end in the system", id)
	}

	doc, err := rdb.Do(ctx, "JSON.GET", id).Text()
	if err != nil {
		t.Fatalf("json.get: %v", err)
	}
	var got homerun.Message
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("decode %q: %v", doc, err)
	}
	if got.System != "radio" || got.Severity != "INFO" || got.Tags != "segment=infra-facts" || got.Title != msg.Title {
		t.Fatalf("stored message = %+v", got)
	}

	// messages and other streams stay untouched
	if n, _ := rdb.XLen(ctx, "messages").Result(); n != 0 {
		t.Errorf("stream messages has %d entries, want 0", n)
	}
	_ = rdb.Del(ctx, stream, id).Err()
}
