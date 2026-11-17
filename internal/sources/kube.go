package sources

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Resources the sources read.
var (
	KustomizationGVR  = schema.GroupVersionResource{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}
	HelmReleaseGVR    = schema.GroupVersionResource{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"}
	VirtualMachineGVR = schema.GroupVersionResource{Group: "kubevirt.io", Version: "v1", Resource: "virtualmachines"}
)

// RestConfig builds a client config from a kubeconfig path, or in-cluster
// when the path is empty.
func RestConfig(kubeconfig, kubeContext string) (*rest.Config, error) {
	if kubeconfig == "" {
		return rest.InClusterConfig()
	}
	rules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: kubeContext}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
}

// Build turns the source configs into sources. Disabled sources are left out;
// a source whose client cannot be built is reported and left out too.
func Build(cfgs []Config, timeout time.Duration) ([]Source, []error) {
	var out []Source
	var errs []error
	for _, c := range cfgs {
		if !c.IsEnabled() {
			continue
		}
		if c.Type == TypePrometheus {
			p, err := NewPrometheus(c, &http.Client{Timeout: timeout})
			if err != nil {
				errs = append(errs, fmt.Errorf("source %s/%s: %w", c.Type, c.Name, err))
				continue
			}
			out = append(out, p)
			continue
		}
		rc, err := RestConfig(c.Kubeconfig, c.Context)
		if err != nil {
			errs = append(errs, fmt.Errorf("source %s/%s: %w", c.Type, c.Name, err))
			continue
		}
		rc.Timeout = timeout
		switch c.Type {
		case TypeKubernetes:
			cs, err := kubernetes.NewForConfig(rc)
			if err != nil {
				errs = append(errs, fmt.Errorf("source %s/%s: %w", c.Type, c.Name, err))
				continue
			}
			out = append(out, &Kubernetes{SourceName: c.Name, Client: cs, Want: c.Facts})
		case TypeFlux, TypeHarvester:
			dc, err := dynamic.NewForConfig(rc)
			if err != nil {
				errs = append(errs, fmt.Errorf("source %s/%s: %w", c.Type, c.Name, err))
				continue
			}
			if c.Type == TypeFlux {
				out = append(out, &Flux{SourceName: c.Name, Client: dc, Want: c.Facts})
			} else {
				out = append(out, &Harvester{SourceName: c.Name, Client: dc, Want: c.Facts})
			}
		}
	}
	return out, errs
}

// Kubernetes reports pods, nodes and namespaces of a cluster.
type Kubernetes struct {
	SourceName string
	Client     kubernetes.Interface
	Want       []string
}

func (k *Kubernetes) Name() string { return k.SourceName }

func (k *Kubernetes) Facts(ctx context.Context) ([]Fact, error) {
	var facts []Fact
	n := k.SourceName
	if wants(k.Want, "pods") {
		pods, err := k.Client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("list pods: %w", err)
		}
		running := 0
		for _, p := range pods.Items {
			if p.Status.Phase == corev1.PodRunning {
				running++
			}
		}
		total := len(pods.Items)
		facts = append(facts, Fact{
			ID: n + "/pods", Source: n,
			Title:   fmt.Sprintf("%d pods on %s", total, n),
			Message: fmt.Sprintf("%s: %d pods, %d of them running", n, total, running),
		})
	}
	if wants(k.Want, "nodes") {
		nodes, err := k.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("list nodes: %w", err)
		}
		ready := 0
		for _, nd := range nodes.Items {
			for _, c := range nd.Status.Conditions {
				if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
					ready++
				}
			}
		}
		total := len(nodes.Items)
		facts = append(facts, Fact{
			ID: n + "/nodes", Source: n,
			Title:   fmt.Sprintf("%s: %s", n, plural(total, "node", "nodes")),
			Message: fmt.Sprintf("%s: %d of %d nodes ready", n, ready, total),
		})
	}
	if wants(k.Want, "namespaces") {
		nss, err := k.Client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("list namespaces: %w", err)
		}
		total := len(nss.Items)
		facts = append(facts, Fact{
			ID: n + "/namespaces", Source: n,
			Title:   fmt.Sprintf("%s: %s", n, plural(total, "namespace", "namespaces")),
			Message: fmt.Sprintf("%s has %d namespaces", n, total),
		})
	}
	return facts, nil
}

// Flux reports how many Kustomizations and HelmReleases are Ready.
type Flux struct {
	SourceName string
	Client     dynamic.Interface
	Want       []string
}

func (f *Flux) Name() string { return f.SourceName }

func (f *Flux) Facts(ctx context.Context) ([]Fact, error) {
	var facts []Fact
	n := f.SourceName
	for _, kind := range []struct {
		fact  string
		gvr   schema.GroupVersionResource
		label string
	}{
		{"kustomizations", KustomizationGVR, "Kustomizations"},
		{"helmreleases", HelmReleaseGVR, "HelmReleases"},
	} {
		if !wants(f.Want, kind.fact) {
			continue
		}
		list, err := f.Client.Resource(kind.gvr).Namespace("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", kind.fact, err)
		}
		total := len(list.Items)
		if total == 0 {
			continue
		}
		ready := 0
		for _, o := range list.Items {
			if conditionTrue(o, "Ready") {
				ready++
			}
		}
		msg := fmt.Sprintf("%s: %d of %d Flux %s ready", n, ready, total, kind.label)
		if ready < total {
			msg += fmt.Sprintf(", %d not ready", total-ready)
		}
		facts = append(facts, Fact{
			ID: n + "/" + kind.fact, Source: n,
			Title:   fmt.Sprintf("Flux %s: %d/%d ready", kind.label, ready, total),
			Message: msg,
		})
	}
	return facts, nil
}

// Harvester reports the virtual machines of a Harvester cluster.
type Harvester struct {
	SourceName string
	Client     dynamic.Interface
	Want       []string
	Now        func() time.Time
}

func (h *Harvester) Name() string { return h.SourceName }

func (h *Harvester) Facts(ctx context.Context) ([]Fact, error) {
	if !wants(h.Want, "vms") && !wants(h.Want, "oldest-vm") {
		return nil, nil
	}
	list, err := h.Client.Resource(VirtualMachineGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list virtualmachines: %w", err)
	}
	n := h.SourceName
	var facts []Fact
	total := len(list.Items)
	if wants(h.Want, "vms") {
		running := 0
		for _, o := range list.Items {
			if s, _, _ := unstructured.NestedString(o.Object, "status", "printableStatus"); s == "Running" {
				running++
			}
		}
		facts = append(facts, Fact{
			ID: n + "/vms", Source: n,
			Title:   fmt.Sprintf("%d VMs on %s", total, n),
			Message: fmt.Sprintf("%s: %d VMs, %d of them running", n, total, running),
		})
	}
	if wants(h.Want, "oldest-vm") && total > 0 {
		items := list.Items
		sort.Slice(items, func(i, j int) bool {
			return items[i].GetCreationTimestamp().Time.Before(items[j].GetCreationTimestamp().Time)
		})
		oldest := items[0]
		now := time.Now
		if h.Now != nil {
			now = h.Now
		}
		days := int(now().Sub(oldest.GetCreationTimestamp().Time).Hours() / 24)
		facts = append(facts, Fact{
			ID: n + "/oldest-vm", Source: n,
			Title:   fmt.Sprintf("Oldest VM: %s", oldest.GetName()),
			Message: fmt.Sprintf("%s: the oldest VM is %s, %s old", n, oldest.GetName(), plural(days, "day", "days")),
		})
	}
	return facts, nil
}

// conditionTrue reports whether status.conditions has type=t with status True.
func conditionTrue(o unstructured.Unstructured, t string) bool {
	conds, _, _ := unstructured.NestedSlice(o.Object, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == t && m["status"] == "True" {
			return true
		}
	}
	return false
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
