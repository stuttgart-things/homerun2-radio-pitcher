package segment

import (
	"context"
	"strings"
	"testing"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/moderation"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
)

const rssWithDescriptions = `<?xml version="1.0"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel>
<item><title>Kubernetes 1.36 released</title><guid>a</guid>
  <description>&lt;p&gt;Kubernetes &lt;b&gt;1.36&lt;/b&gt; brings 45 enhancements &amp;amp; sidecars.&lt;/p&gt;</description></item>
<item><title>Only content</title><guid>b</guid>
  <content:encoded><![CDATA[<div>From <a href="x">the blog</a>:   long   text</div>]]></content:encoded></item>
<item><title>Nothing</title><guid>c</guid></item>
</channel></rss>`

const atomWithSummary = `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
<entry><title>Flux 2.8</title><id>f</id><summary type="html">&lt;p&gt;Flux 2.8 adds OCI signing.&lt;/p&gt;</summary></entry>
</feed>`

func TestFeedDescriptions(t *testing.T) {
	hs, err := ParseFeed("k8s", strings.NewReader(rssWithDescriptions))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Kubernetes 1.36 brings 45 enhancements & sidecars.", "From the blog : long text", ""}
	for i, h := range hs {
		if h.Description != want[i] {
			t.Errorf("rss %d: %q, want %q", i, h.Description, want[i])
		}
	}
	hs, err = ParseFeed("flux", strings.NewReader(atomWithSummary))
	if err != nil || hs[0].Description != "Flux 2.8 adds OCI signing." {
		t.Fatalf("atom: %+v, %v", hs, err)
	}
	long := cleanDescription(strings.Repeat("word ", 400))
	if n := len([]rune(long)); n > descriptionMax || n < descriptionMax/2 || strings.HasSuffix(long, " ") {
		t.Errorf("capped description: %d runes", n)
	}
}

func TestNewsSummary(t *testing.T) {
	news := func(answer string) *News {
		n := &News{Feeds: []Feed{{Name: "k8s"}}, Title: panel.Card4x6, MessageMax: 120,
			Fetch: func(_ context.Context, f Feed) ([]Headline, error) {
				return ParseFeed(f.Name, strings.NewReader(rssWithDescriptions))
			}}
		if answer != "" {
			n.Moderator = &moderation.Moderator{Client: answerFor{summary: answer}}
		}
		return n
	}
	on := schedule.Params{"summary": true, "count": 3}

	items, _ := news("Kubernetes 1.36 ships 45 enhancements, sidecars included.").Build(context.Background(), now, on)
	if items[0].Message != "k8s: Kubernetes 1.36 ships 45 enhancements, sidecars included." {
		t.Errorf("summary: %q", items[0].Message)
	}
	// no description: the headline, and no model call for it
	if items[2].Message != "k8s: Nothing" {
		t.Errorf("no description: %q", items[2].Message)
	}

	for _, bad := range []string{
		"Kubernetes 1.36 ships 50 enhancements.", // a number not in the item
		"SKIP",                                   // nothing to sum up
		"Kubernetes 1.36 released",               // only the headline again
	} {
		items, _ := news(bad).Build(context.Background(), now, on)
		if items[0].Message != "k8s: Kubernetes 1.36 released" {
			t.Errorf("answer %q aired: %q", bad, items[0].Message)
		}
	}
	// not asked, or no model: the headline
	if items, _ := news("Kubernetes 1.36 ships 45 enhancements.").Build(context.Background(), now, schedule.Params{"count": 1}); items[0].Message != "k8s: Kubernetes 1.36 released" {
		t.Errorf("not asked: %q", items[0].Message)
	}
	if items, _ := news("").Build(context.Background(), now, on); items[0].Message != "k8s: Kubernetes 1.36 released" {
		t.Errorf("no model: %q", items[0].Message)
	}
}

// answerFor answers summary prompts with summary and anything else (the
// headline shortening) with an empty answer, so the title falls back.
type answerFor struct{ summary string }

func (a answerFor) Complete(_ context.Context, system, _ string) (string, error) {
	if strings.Contains(system, "sum up a news item") {
		return a.summary, nil
	}
	return "", nil
}
