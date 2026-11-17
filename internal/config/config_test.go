package config

import (
	"strings"
	"testing"
	"time"
)

func getenv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := Load(getenv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Stream != "ambient" || c.System != "radio" || c.Severity != "INFO" || c.LLMEnabled || c.DryRun {
		t.Fatalf("defaults = %+v", c)
	}
	if c.Location.String() != "Europe/Berlin" || c.Spacing != 15*time.Second || c.Title.Lines != 4 || c.Title.Width != 16 {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestOverrides(t *testing.T) {
	c, err := Load(getenv(map[string]string{
		"REDIS_ADDR": "redis-stack", "REDIS_PASSWORD": "pw", "REDIS_STREAM": "ambient2",
		"RADIO_TIMEZONE": "UTC", "RADIO_ITEM_SPACING": "20s", "RADIO_TITLE_LINES": "3", "RADIO_TITLE_WIDTH": "12",
		"RADIO_LLM_ENABLED": "true", "OLLAMA_URL": "http://ollama:11434", "OLLAMA_MODEL": "llama3.2", "OLLAMA_API": "native",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Redis.Addr != "redis-stack" || c.Redis.Password != "pw" || c.Stream != "ambient2" || c.Location != time.UTC {
		t.Fatalf("%+v", c)
	}
	if c.Spacing != 20*time.Second || c.Title.Lines != 3 || !c.LLMEnabled || c.OllamaAPI != "native" {
		t.Fatalf("%+v", c)
	}
}

func TestErrors(t *testing.T) {
	_, err := Load(getenv(map[string]string{
		"RADIO_TIMEZONE": "Mars/Base", "RADIO_ITEM_SPACING": "soon", "RADIO_TITLE_LINES": "x",
		"RADIO_DRY_RUN": "maybe", "OLLAMA_API": "openrouter", "RADIO_LLM_ENABLED": "true",
		"BASE_PATH": "/radio/../x", "RADIO_UI_TOKEN": "short",
	}))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"RADIO_TIMEZONE", "RADIO_ITEM_SPACING", "RADIO_TITLE_LINES", "RADIO_DRY_RUN", "OLLAMA_API", "OLLAMA_URL", "BASE_PATH", "RADIO_UI_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %s: %v", want, err)
		}
	}
}

func TestParseBasePath(t *testing.T) {
	for in, want := range map[string]string{"": "", "/": "", "radio": "/radio", "/radio/": "/radio", " /a/b ": "/a/b"} {
		if got, err := ParseBasePath(in); err != nil || got != want {
			t.Errorf("ParseBasePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"/a/../b", "/\"x", "/a b"} {
		if _, err := ParseBasePath(bad); err == nil {
			t.Errorf("ParseBasePath(%q) accepted", bad)
		}
	}
}
