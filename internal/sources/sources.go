// Package sources collects infrastructure facts for the infra-facts segment.
// Every number in a fact comes from a real query; a source that fails is
// skipped and the others still report.
package sources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Fact is one thing worth saying on air. Title and Message are the fixed
// texts; the moderator may reword Message, never the numbers in it.
type Fact struct {
	ID      string // stable identity, e.g. "homerun2-dev/pods", to avoid repeats
	Source  string // source name
	Title   string // card title, short
	Message string // scrolling line, holds every number of the fact
}

// Source reports facts.
type Source interface {
	Name() string
	Facts(ctx context.Context) ([]Fact, error)
}

// Types of source.
const (
	TypeKubernetes = "kubernetes"
	TypeFlux       = "flux"
	TypeHarvester  = "harvester"
)

// Config is one entry of sources.yaml.
type Config struct {
	// Name is how the source is called on air, e.g. the cluster name.
	Name string `yaml:"name"`
	// Type is kubernetes, flux or harvester.
	Type string `yaml:"type"`
	// Enabled switches the source; unset means on.
	Enabled *bool `yaml:"enabled"`
	// Kubeconfig is the path to a kubeconfig; empty means in-cluster.
	Kubeconfig string `yaml:"kubeconfig"`
	// Context selects a kubeconfig context; empty means the current one.
	Context string `yaml:"context"`
	// Facts limits the facts of the source; empty means all of its type.
	Facts []string `yaml:"facts"`
}

// IsEnabled reports whether the source is switched on.
func (c Config) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// FactsOf lists the facts each type can report.
var FactsOf = map[string][]string{
	TypeKubernetes: {"pods", "nodes", "namespaces"},
	TypeFlux:       {"kustomizations", "helmreleases"},
	TypeHarvester:  {"vms", "oldest-vm"},
}

type file struct {
	Sources []Config `yaml:"sources"`
}

// LoadConfig reads sources.yaml.
func LoadConfig(path string) ([]Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseConfig(data)
}

// ParseConfig reads and validates the source list.
func ParseConfig(data []byte) ([]Config, error) {
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("sources: %w", err)
	}
	var errs []error
	seen := map[string]bool{}
	for i, c := range f.Sources {
		where := fmt.Sprintf("sources[%d]", i)
		if c.Name == "" {
			errs = append(errs, fmt.Errorf("%s: no name", where))
			continue
		}
		where += " " + c.Name
		known, ok := FactsOf[c.Type]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: type %q, want one of kubernetes, flux, harvester", where, c.Type))
			continue
		}
		key := c.Type + "/" + c.Name
		if seen[key] {
			errs = append(errs, fmt.Errorf("%s: %s source named %q given twice", where, c.Type, c.Name))
		}
		seen[key] = true
		for _, fact := range c.Facts {
			if !slices.Contains(known, fact) {
				errs = append(errs, fmt.Errorf("%s: fact %q, %s knows %s", where, fact, c.Type, strings.Join(known, ", ")))
			}
		}
	}
	return f.Sources, errors.Join(errs...)
}

// wants reports whether fact is selected by the list (empty: all).
func wants(list []string, fact string) bool {
	return len(list) == 0 || slices.Contains(list, fact)
}
