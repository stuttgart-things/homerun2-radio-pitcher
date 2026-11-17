package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeProm answers instant queries from a map query -> JSON body.
func fakeProm(t *testing.T, answers map[string]string, auth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			http.NotFound(w, r)
			return
		}
		if auth != nil {
			*auth = r.Header.Get("Authorization")
		}
		body, ok := answers[r.URL.Query().Get("query")]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func vector(samples ...string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(samples, ",") + `]}}`
}

func promSource(t *testing.T, url string, qs ...Query) *Prometheus {
	t.Helper()
	p, err := NewPrometheus(Config{Name: "dev", Type: TypePrometheus, URL: url, Queries: qs}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPrometheusFacts(t *testing.T) {
	srv := fakeProm(t, map[string]string{
		"restarts": vector(`{"metric":{},"value":[1763380800,"7"]}`),
		"cpu":      vector(`{"metric":{"node":"b"},"value":[1763380800,"0.4567"]}`, `{"metric":{"node":"a"},"value":[1763380800,"12.3"]}`),
		"empty":    vector(),
		"scalar":   `{"status":"success","data":{"resultType":"scalar","result":[1763380800,"42"]}}`,
		"nan":      vector(`{"metric":{},"value":[1763380800,"NaN"]}`),
	}, nil)
	p := promSource(t, srv.URL+"/",
		Query{ID: "restarts", Query: "restarts", Title: "Restarts: {{.Value}}", Message: "{{.Source}}: {{.Value}} pod restarts in the last hour"},
		Query{ID: "cpu", Query: "cpu", Decimals: 1, Message: "{{.Labels.node}} at {{.Value}}% CPU{{if gt .Float 10.0}}, busy{{end}}"},
		Query{ID: "empty", Query: "empty", Message: "never"},
		Query{ID: "scalar", Query: "scalar", Message: "answer {{.Value}}{{.Labels.missing}}"},
		Query{ID: "nan", Query: "nan", Message: "never"},
	)
	facts, err := p.Facts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := byID(facts)
	if len(facts) != 4 {
		t.Fatalf("facts = %+v", facts)
	}
	if f := got["dev/restarts"]; f.Title != "Restarts: 7" || f.Message != "dev: 7 pod restarts in the last hour" || f.Source != "dev" {
		t.Errorf("restarts = %+v", f)
	}
	if f := got["dev/cpu/node=a"]; f.Message != "a at 12.3% CPU, busy" || f.Title != f.Message {
		t.Errorf("cpu a = %+v", f)
	}
	if f := got["dev/cpu/node=b"]; f.Message != "b at 0.5% CPU" {
		t.Errorf("cpu b = %+v", f)
	}
	if f := got["dev/scalar"]; f.Message != "answer 42" {
		t.Errorf("scalar = %+v", f)
	}
}

func TestPrometheusLimitWantAndErrors(t *testing.T) {
	srv := fakeProm(t, map[string]string{
		"many": vector(
			`{"metric":{"ns":"c"},"value":[0,"3"]}`,
			`{"metric":{"ns":"a"},"value":[0,"1"]}`,
			`{"metric":{"ns":"b"},"value":[0,"2"]}`,
		),
	}, nil)
	many := Query{ID: "many", Query: "many", Limit: 2, Message: "{{.Labels.ns}}={{.Value}}"}
	broken := Query{ID: "broken", Query: "sum(", Message: "x"}

	// limit keeps the first samples in label order
	p := promSource(t, srv.URL, many, broken)
	facts, err := p.Facts(context.Background())
	if err != nil {
		t.Fatal("one failing query must not fail the source:", err)
	}
	if len(facts) != 2 || facts[0].Message != "a=1" || facts[1].Message != "b=2" {
		t.Fatalf("facts = %+v", facts)
	}

	// every wanted query fails: error with the API's message
	p.Want = []string{"broken"}
	if _, err := p.Facts(context.Background()); err == nil || !strings.Contains(err.Error(), "parse error") {
		t.Fatalf("err = %v", err)
	}

	// unreachable
	p = promSource(t, "http://127.0.0.1:1", many)
	p.HTTP = &http.Client{Timeout: time.Second}
	if _, err := p.Facts(context.Background()); err == nil {
		t.Fatal("unreachable Prometheus did not fail")
	}
}

func TestPrometheusToken(t *testing.T) {
	var auth string
	srv := fakeProm(t, map[string]string{"up": vector(`{"metric":{},"value":[0,"1"]}`)}, &auth)
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := promSource(t, srv.URL, Query{ID: "up", Query: "up", Message: "{{.Value}} up"})
	p.TokenFile = tok
	if _, err := p.Facts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer s3cret" {
		t.Fatalf("Authorization = %q", auth)
	}
	p.TokenFile = filepath.Join(t.TempDir(), "missing")
	if _, err := p.Facts(context.Background()); err == nil {
		t.Fatal("missing token file did not fail")
	}
}

func TestParseConfigPrometheus(t *testing.T) {
	ok := `
sources:
  - name: dev
    type: prometheus
    url: http://prometheus:9090
    facts: [restarts]
    queries:
      - id: restarts
        query: sum(increase(kube_pod_container_status_restarts_total[1h]))
        message: "{{.Source}}: {{.Value}} restarts"
`
	if _, err := ParseConfig([]byte(ok)); err != nil {
		t.Fatal(err)
	}
	bad := `
sources:
  - name: dev
    type: prometheus
    url: prometheus:9090
    facts: [nope]
    queries:
      - id: a
        query: up
        message: "{{.Value"
      - id: a
        query: up
        message: ok
      - id: b
        message: no query
      - id: c
        query: up
        message: ok
        decimals: 9
  - name: empty
    type: prometheus
`
	_, err := ParseConfig([]byte(bad))
	if err == nil {
		t.Fatal("bad config accepted")
	}
	for _, want := range []string{"url \"prometheus:9090\"", "message:", "id \"a\" given twice", "needs id, query and message", "decimals 9", "fact \"nope\"", "empty: prometheus needs url", "empty: prometheus needs queries"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestBuildPrometheus(t *testing.T) {
	srcs, errs := Build([]Config{{
		Name: "dev", Type: TypePrometheus, URL: "http://prometheus:9090",
		Queries: []Query{{ID: "up", Query: "up", Message: "{{.Value}}"}},
	}}, time.Second)
	if len(errs) != 0 || len(srcs) != 1 || srcs[0].Name() != "dev" {
		t.Fatalf("srcs = %v, errs = %v", srcs, errs)
	}
}
