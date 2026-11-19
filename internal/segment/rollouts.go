package segment

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/panel"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/schedule"
	"github.com/stuttgart-things/homerun2-radio-pitcher/internal/sources"
)

// Rollouts airs what was deployed lately: workloads with a new revision and
// upgraded Helm releases of the clusters in sources.yaml (kubernetes and
// flux sources), newest first. Rollouts of one namespace within
// rolloutGroup make one card (an operator rolling out five deployments at
// once). A rollout airs once. Fixed texts: every name, version and time
// comes from the cluster.
//
// Parameters: count (default 1), within (Go duration, default 6h: how far
// back), namespaces (default all), kinds (deployment, statefulset,
// daemonset, helmrelease; default all).
type Rollouts struct {
	Sources    []sources.RolloutSource
	Title      panel.Title
	MessageMax int
	Timeout    time.Duration
	// SeenFor is how long an aired rollout is not repeated (default 48h).
	SeenFor time.Duration
	Log     *slog.Logger

	mu   sync.Mutex
	seen map[string]time.Time // rollout ID -> when aired
}

func (r *Rollouts) Name() string { return "rollouts" }

func (r *Rollouts) Build(ctx context.Context, now time.Time, p schedule.Params) ([]Item, error) {
	if len(r.Sources) == 0 {
		return nil, nil
	}
	since := now.Add(-p.Duration("within", 6*time.Hour))
	namespaces := p.Strings("namespaces")
	kinds := p.Strings("kinds")

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		all  []sources.Rollout
		errs []error
	)
	for _, s := range r.Sources {
		wg.Add(1)
		go func(s sources.RolloutSource) {
			defer wg.Done()
			rs, err := s.Rollouts(cctx, since)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
				r.logger().Warn("rollout source failed, skipped", "source", s.Name(), "error", err)
				return
			}
			all = append(all, rs...)
		}(s)
	}
	wg.Wait()
	if len(errs) == len(r.Sources) {
		return nil, fmt.Errorf("rollouts: every source failed: %v", errs)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.After(all[j].At) })

	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(now)

	var fresh []sources.Rollout
	for _, ro := range all {
		if len(namespaces) > 0 && !slices.Contains(namespaces, ro.Namespace) {
			continue
		}
		if len(kinds) > 0 && !slices.Contains(kinds, ro.Kind) {
			continue
		}
		if _, ok := r.seen[ro.ID]; ok {
			continue
		}
		fresh = append(fresh, ro)
	}

	count := p.Int("count", 1)
	var items []Item
	for _, group := range groupRollouts(fresh) {
		if len(items) == count {
			break
		}
		if !IsPreview(ctx) {
			for _, ro := range group {
				r.seen[ro.ID] = now
			}
		}
		title, msg := rolloutText(group[0], now.Location())
		if len(group) > 1 {
			title, msg = groupText(group, now.Location())
		}
		items = append(items, Item{Title: r.Title.Fit(title), Message: panel.Truncate(msg, r.MessageMax)})
	}
	return items, nil
}

// rolloutGroup is how close in time rollouts of one namespace must be to
// share a card.
const rolloutGroup = 10 * time.Minute

// groupRollouts groups rollouts (newest first) of the same source and
// namespace that lie within rolloutGroup of the group's newest; the groups
// keep the order of their newest rollout.
func groupRollouts(rs []sources.Rollout) [][]sources.Rollout {
	var groups [][]sources.Rollout
	for _, ro := range rs {
		placed := false
		for i, g := range groups {
			if g[0].Source == ro.Source && g[0].Namespace == ro.Namespace && g[0].At.Sub(ro.At) <= rolloutGroup {
				groups[i] = append(g, ro)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, []sources.Rollout{ro})
		}
	}
	return groups
}

// groupText is the card for several rollouts in one namespace:
//
//	Deployed: 5 in argocd
//	labul: 5 rollouts in argocd at 12:15: argocd-server v3.6.2, argocd-repo-server v3.6.2, …
func groupText(g []sources.Rollout, loc *time.Location) (title, msg string) {
	first := g[0]
	names := make([]string, len(g))
	for i, ro := range g {
		names[i] = strings.TrimSpace(ro.Name + " " + ro.Version)
	}
	return fmt.Sprintf("Deployed: %d in %s", len(g), first.Namespace),
		fmt.Sprintf("%s: %d rollouts in %s at %s: %s", first.Source, len(g), first.Namespace, first.At.In(loc).Format("15:04"), strings.Join(names, ", "))
}

// rolloutText is the fixed card for a rollout.
//
//	Deployed: homerun2-radio-pitcher v0.14.0
//	homerun2-dev: homerun2-radio-pitcher v0.14.0 rolled out in homerun2 at 14:05
//	homerun2-dev: Helm release cert-manager upgraded to chart 1.15.0 in cert-manager at 14:05
func rolloutText(ro sources.Rollout, loc *time.Location) (title, msg string) {
	at := ro.At.In(loc).Format("15:04")
	version := ""
	if ro.Version != "" {
		version = " " + ro.Version
	}
	if ro.Kind == sources.KindHelmRelease {
		verb := "installed"
		if ro.Upgrade {
			verb = "upgraded"
		}
		chart := ""
		if ro.Version != "" {
			chart = " to chart " + ro.Version
			if !ro.Upgrade {
				chart = " with chart " + ro.Version
			}
		}
		return "Helm: " + ro.Name + version,
			fmt.Sprintf("%s: Helm release %s %s%s in %s at %s", ro.Source, ro.Name, verb, chart, ro.Namespace, at)
	}
	verb := "rolled out"
	if !ro.Upgrade {
		verb = "deployed"
	}
	return "Deployed: " + ro.Name + version,
		strings.TrimSpace(fmt.Sprintf("%s: %s%s %s in %s at %s", ro.Source, ro.Name, version, verb, ro.Namespace, at))
}

func (r *Rollouts) prune(now time.Time) {
	if r.seen == nil {
		r.seen = map[string]time.Time{}
	}
	ttl := r.SeenFor
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	for k, at := range r.seen {
		if now.Sub(at) > ttl {
			delete(r.seen, k)
		}
	}
}

func (r *Rollouts) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}
