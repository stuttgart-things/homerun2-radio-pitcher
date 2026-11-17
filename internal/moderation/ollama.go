// Package moderation lets a local language model phrase and shorten segment
// texts. The model only rewords: every number it returns must already be in
// its input, and anything that fails a check falls back to the fixed text.
package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client completes one chat turn.
type Client interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// API selects the Ollama endpoint.
const (
	APIOpenAI = "openai" // POST /v1/chat/completions (OpenAI compatible)
	APINative = "native" // POST /api/chat
)

// Ollama talks to a local Ollama server, or to any OpenAI-compatible
// endpoint (e.g. OpenRouter at https://openrouter.ai/api) with APIKey.
type Ollama struct {
	URL         string // base URL, e.g. http://ollama.ollama.svc:11434
	Model       string // e.g. llama3.2:3b
	API         string // APIOpenAI (default) or APINative
	APIKey      string // optional, sent as "Authorization: Bearer"
	Temperature float64
	HTTP        *http.Client
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Complete sends the system and user prompt and returns the answer.
func (o *Ollama) Complete(ctx context.Context, system, user string) (string, error) {
	if o.URL == "" || o.Model == "" {
		return "", errors.New("ollama: url and model are required")
	}
	msgs := []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}}
	base := strings.TrimRight(o.URL, "/")

	var (
		path string
		body any
	)
	if o.API == APINative {
		path = "/api/chat"
		body = map[string]any{
			"model":    o.Model,
			"messages": msgs,
			"stream":   false,
			"options":  map[string]any{"temperature": o.Temperature},
		}
	} else {
		path = "/v1/chat/completions"
		body = map[string]any{
			"model":       o.Model,
			"messages":    msgs,
			"stream":      false,
			"temperature": o.Temperature,
		}
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	hc := o.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("ollama: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}

	if o.API == APINative {
		var r struct {
			Message chatMessage `json:"message"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return "", fmt.Errorf("ollama: decode: %w", err)
		}
		return r.Message.Content, nil
	}
	var r struct {
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("ollama: decode: %w", err)
	}
	if len(r.Choices) == 0 {
		return "", errors.New("ollama: no choices in the answer")
	}
	return r.Choices[0].Message.Content, nil
}
