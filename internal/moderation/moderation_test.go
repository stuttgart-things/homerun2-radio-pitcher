package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
)

type fakeClient struct {
	answer string
	err    error
	calls  int
}

func (f *fakeClient) Complete(context.Context, string, string) (string, error) {
	f.calls++
	return f.answer, f.err
}

const long = "Kubernetes 1.36 brings new features for sidecar containers and more security"

func TestHeadlineWithoutModelFallsBack(t *testing.T) {
	var m *Moderator
	if got, want := m.Headline(context.Background(), long, panel.Card4x6), panel.Card4x6.Fit(long); got != want {
		t.Fatalf("nil moderator: %q, want %q", got, want)
	}
	m = &Moderator{}
	if got, want := m.Headline(context.Background(), long, panel.Card4x6), panel.Card4x6.Fit(long); got != want {
		t.Fatalf("no client: %q, want %q", got, want)
	}
}

func TestHeadlineUsesModel(t *testing.T) {
	c := &fakeClient{answer: "\"Kubernetes 1.36: sidecars and security\"\n"}
	m := &Moderator{Client: c}
	got := m.Headline(context.Background(), long, panel.Card4x6)
	if got != "Kubernetes 1.36: sidecars and security" {
		t.Fatalf("got %q", got)
	}
}

func TestHeadlineSkipsModelWhenItFits(t *testing.T) {
	c := &fakeClient{answer: "anything"}
	m := &Moderator{Client: c}
	if got := m.Headline(context.Background(), "Go 1.27 is out", panel.Card5x7); got != "Go 1.27 is out" {
		t.Fatalf("got %q", got)
	}
	if c.calls != 0 {
		t.Fatal("model called for a headline that fits")
	}
}

func TestHeadlineFallbacks(t *testing.T) {
	cases := map[string]*fakeClient{
		"error":           {err: errors.New("connection refused")},
		"empty":           {answer: "  \n"},
		"too long":        {answer: strings.Repeat("long ", 30)},
		"invented number": {answer: "Kubernetes 2.0 is out"},
	}
	want := panel.Card4x6.Fit(long)
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := &Moderator{Client: c}
			if got := m.Headline(context.Background(), long, panel.Card4x6); got != want {
				t.Fatalf("got %q, want fallback %q", got, want)
			}
		})
	}
}

func TestPhrase(t *testing.T) {
	tmpl := "homerun2-dev: 312 pods, 298 running"
	m := &Moderator{Client: &fakeClient{answer: "298 of 312 pods are running on homerun2-dev."}}
	if got := m.Phrase(context.Background(), tmpl, 120); got != "298 of 312 pods are running on homerun2-dev." {
		t.Fatalf("got %q", got)
	}
	// computed number: 312-298 = 14 is not in the source
	m = &Moderator{Client: &fakeClient{answer: "14 pods are not running on homerun2-dev."}}
	if got := m.Phrase(context.Background(), tmpl, 120); got != tmpl {
		t.Fatalf("invented number not rejected: %q", got)
	}
	m = &Moderator{Client: &fakeClient{answer: strings.Repeat("x", 50)}}
	if got := m.Phrase(context.Background(), tmpl, 20); got != tmpl {
		t.Fatalf("too long not rejected: %q", got)
	}
	var nilM *Moderator
	if got := nilM.Phrase(context.Background(), tmpl, 0); got != tmpl {
		t.Fatalf("nil: %q", got)
	}
}

func TestNumbersFrom(t *testing.T) {
	if err := NumbersFrom("41 of 42", "Flux: 41/42 Ready"); err != nil {
		t.Fatal(err)
	}
	if err := NumbersFrom("007 agents", "7 agents"); err != nil {
		t.Fatal("leading zeros are the same number:", err)
	}
	if err := NumbersFrom("no numbers", "12 pods"); err != nil {
		t.Fatal("dropping numbers is allowed:", err)
	}
	if err := NumbersFrom("13 pods", "12 pods"); err == nil {
		t.Fatal("changed number accepted")
	}
}

func TestOllamaOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Model    string        `json:"model"`
			Messages []chatMessage `json:"messages"`
			Stream   bool          `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "llama3.2" || len(body.Messages) != 2 || body.Stream {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"short"}}]}`))
	}))
	defer srv.Close()

	o := &Ollama{URL: srv.URL + "/", Model: "llama3.2"}
	got, err := o.Complete(context.Background(), "sys", "user")
	if err != nil || got != "short" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestOllamaAPIKey(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	for _, key := range []string{"sk-or-test", ""} {
		o := &Ollama{URL: srv.URL, Model: "m", APIKey: key}
		if _, err := o.Complete(context.Background(), "sys", "user"); err != nil {
			t.Fatal(err)
		}
	}
	if auth[0] != "Bearer sk-or-test" || auth[1] != "" {
		t.Fatalf("Authorization headers: %q", auth)
	}
}

func TestOllamaNative(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"native"}}`))
	}))
	defer srv.Close()

	o := &Ollama{URL: srv.URL, Model: "m", API: APINative}
	got, err := o.Complete(context.Background(), "sys", "user")
	if err != nil || got != "native" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestOllamaErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := (&Ollama{URL: srv.URL, Model: "m"}).Complete(context.Background(), "s", "u"); err == nil {
		t.Fatal("expected error on 404")
	}
	if _, err := (&Ollama{}).Complete(context.Background(), "s", "u"); err == nil {
		t.Fatal("expected error without url")
	}
	// unreachable server: the moderator falls back
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	m := &Moderator{Client: &Ollama{URL: dead.URL, Model: "m"}}
	if got, want := m.Headline(context.Background(), long, panel.Card5x7), panel.Card5x7.Fit(long); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
