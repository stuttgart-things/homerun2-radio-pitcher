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

func TestNumbersFromWords(t *testing.T) {
	for _, tc := range []struct {
		out, source string
		ok          bool
	}{
		// #11: invented as words
		{"Six of the sixty-four VMs are running.", "harvester: 6 VMs, 6 of them running", false},
		{"Nine of nine Flux HelmReleases are ready.", "homerun2-dev: 9 of 9 Flux HelmReleases ready", false},
		// #11: 41 used twice, 42 dropped
		{"41 of 41 Flux Kustomizations are ready to go.", "homerun2-dev: 41 of 42 Flux Kustomizations ready, 1 not ready", false},
		{"9 of 9 Flux HelmReleases are ready.", "homerun2-dev: 9 of 9 Flux HelmReleases ready", true},
		{"Twenty new projects join the CNCF", "Twenty new projects join the CNCF sandbox", true},
		{"Three new projects", "Two new projects join", false},
		{"A dozen talks at KubeCon", "12 talks at KubeCon", false},
		{"Vierundsechzig Pods laufen", "64 Pods laufen", false},
		{"Zwölf Knoten bereit", "12 Knoten bereit", false},
		{"Nacht der offenen Tür, Achtung", "Nacht der offenen Tür", true},
		{"Someone done it often", "it", true},
	} {
		err := NumbersFrom(tc.out, tc.source)
		if (err == nil) != tc.ok {
			t.Errorf("NumbersFrom(%q, %q) = %v, want ok=%v", tc.out, tc.source, err, tc.ok)
		}
	}
}

func TestPhraseKeepsSourceName(t *testing.T) {
	tmpl := "homerun2-dev: 41 of 42 Flux Kustomizations ready, 1 not ready"
	m := &Moderator{Client: &fakeClient{answer: "41 of 42 Kustomizations are ready, 1 is not."}}
	if got := m.Phrase(context.Background(), tmpl, 120, "homerun2-dev"); got != tmpl {
		t.Fatalf("dropped source name not rejected: %q", got)
	}
	m = &Moderator{Client: &fakeClient{answer: "On Homerun2-Dev, 41 of 42 Kustomizations are ready, 1 is not."}}
	if got := m.Phrase(context.Background(), tmpl, 120, "homerun2-dev"); got == tmpl {
		t.Fatal("answer with source name rejected")
	}
	// a template without the name (e.g. a prometheus query text) does not need it
	tmpl = "clusterscope: 4 warning findings in labul"
	m = &Moderator{Client: &fakeClient{answer: "labul has 4 warning findings in clusterscope."}}
	if got := m.Phrase(context.Background(), tmpl, 120, "homerun2-dev"); got == tmpl {
		t.Fatal("name required although the template lacks it")
	}
}

func TestBlurb(t *testing.T) {
	tmpl := "homerun2-scout: Analytics for homerun2 messages, with 3 digest windows"
	for _, tc := range []struct {
		answer string
		ok     bool
	}{
		{"Did you know homerun2-scout counts homerun2 messages in 3 digest windows?", true},
		{"Did you know the scout counts messages in 3 windows?", false},                     // name dropped
		{"Did you know homerun2-scout keeps 30 days of messages?", false},                   // invented number
		{"Did you know homerun2-scout " + strings.Repeat("really ", 30) + "counts?", false}, // too long
	} {
		m := &Moderator{Client: &fakeClient{answer: tc.answer}}
		got := m.Blurb(context.Background(), tmpl, "homerun2-scout", 120)
		if (got == tc.answer) != tc.ok {
			t.Errorf("answer %q: aired %q, want accepted=%v", tc.answer, got, tc.ok)
		}
		if !tc.ok && got != tmpl {
			t.Errorf("rejected answer: aired %q, want the template", got)
		}
	}
	var m *Moderator
	if got := m.Blurb(context.Background(), tmpl, "homerun2-scout", 120); got != tmpl {
		t.Fatalf("no model: %q", got)
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

func TestObserveReportsEveryOutcome(t *testing.T) {
	const fact = "platform-sthings runs 64 pods on 3 nodes."
	cases := []struct {
		name, want string
		client     *fakeClient
		call       func(m *Moderator)
	}{
		{"fact used", ResultUsed, &fakeClient{answer: "platform-sthings: 64 pods, 3 nodes."},
			func(m *Moderator) { m.Phrase(context.Background(), fact, 120, "platform-sthings") }},
		{"fact number", ResultRejectedNumber, &fakeClient{answer: "platform-sthings runs 65 pods."},
			func(m *Moderator) { m.Phrase(context.Background(), fact, 120, "platform-sthings") }},
		{"fact name", ResultRejectedName, &fakeClient{answer: "The cluster runs 64 pods."},
			func(m *Moderator) { m.Phrase(context.Background(), fact, 120, "platform-sthings") }},
		{"fact length", ResultRejectedLength, &fakeClient{answer: "platform-sthings is busy with 64 pods on 3 nodes today."},
			func(m *Moderator) { m.Phrase(context.Background(), fact, 20, "platform-sthings") }},
		{"fact empty", ResultRejectedEmpty, &fakeClient{answer: " \"\" "},
			func(m *Moderator) { m.Phrase(context.Background(), fact, 120) }},
		{"fact echo", ResultRejectedEcho, &fakeClient{answer: "Platform-sthings runs 64 pods on 3 nodes."},
			func(m *Moderator) { m.Phrase(context.Background(), fact, 120) }},
		{"intro used", ResultUsed, &fakeClient{answer: "Grab a coffee, the news is next!"},
			func(m *Moderator) { m.Intro(context.Background(), "News", "the latest headlines", 120) }},
		{"time number", ResultRejectedNumber, &fakeClient{answer: "It's 15:00 on Tuesday, 2 hours to go."},
			func(m *Moderator) { m.TimeLine(context.Background(), "It's 15:00 on Tuesday.", 120, "15:00") }},
		{"fact error", ResultError, &fakeClient{err: errors.New("429 Too Many Requests")},
			func(m *Moderator) { m.Phrase(context.Background(), fact, 120) }},
		{"headline length", ResultRejectedLength, &fakeClient{answer: long},
			func(m *Moderator) { m.Headline(context.Background(), long, panel.Card4x6) }},
		{"blurb name", ResultRejectedName, &fakeClient{answer: "A service for analytics."},
			func(m *Moderator) {
				m.Blurb(context.Background(), "homerun2-scout: Analytics", "homerun2-scout", 120)
			}},
	}
	for _, c := range cases {
		var kinds, results []string
		m := &Moderator{Client: c.client, Observe: func(kind, result string) {
			kinds = append(kinds, kind)
			results = append(results, result)
		}}
		c.call(m)
		if len(results) != 1 || results[0] != c.want {
			t.Errorf("%s: results %v, want [%s]", c.name, results, c.want)
		}
		wantKind := strings.Fields(c.name)[0]
		if len(kinds) == 1 && kinds[0] != wantKind {
			t.Errorf("%s: kind %q, want %q", c.name, kinds[0], wantKind)
		}
	}

	// no model call, nothing observed: a headline that fits, or no client
	var n int
	m := &Moderator{Client: &fakeClient{answer: "x"}, Observe: func(string, string) { n++ }}
	m.Headline(context.Background(), "Go 1.27 is out", panel.Card5x7)
	(&Moderator{Observe: func(string, string) { n++ }}).Phrase(context.Background(), fact, 120)
	if n != 0 {
		t.Fatalf("observed %d calls that never reached the model", n)
	}
}
