package sources

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func TestParseConfig(t *testing.T) {
	cfgs, err := ParseConfig([]byte(`
sources:
  - name: homerun2-dev
    type: kubernetes
  - name: homerun2-dev
    type: flux
    kubeconfig: /k/dev
    facts: [kustomizations]
  - name: harvester
    type: harvester
    enabled: false
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfgs) != 3 || cfgs[1].Kubeconfig != "/k/dev" || cfgs[2].IsEnabled() || !cfgs[0].IsEnabled() {
		t.Fatalf("cfgs = %+v", cfgs)
	}
}

func TestParseConfigErrors(t *testing.T) {
	_, err := ParseConfig([]byte(`
sources:
  - type: kubernetes
  - name: a
    type: prometheus
  - name: b
    type: flux
    facts: [pods]
  - name: c
    type: kubernetes
  - name: c
    type: kubernetes
`))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"no name", "prometheus", `fact "pods"`, "twice"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestKubernetesFacts(t *testing.T) {
	cs := fake.NewClientset(
		pod("a", corev1.PodRunning), pod("b", corev1.PodRunning), pod("c", corev1.PodPending),
		node("n1", corev1.ConditionTrue), node("n2", corev1.ConditionFalse),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
	)
	k := &Kubernetes{SourceName: "dev", Client: cs}
	facts, err := k.Facts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := byID(facts)
	if f := got["dev/pods"]; f.Title != "3 pods on dev" || f.Message != "dev: 3 pods, 2 of them running" {
		t.Errorf("pods = %+v", f)
	}
	if f := got["dev/nodes"]; f.Message != "dev: 1 of 2 nodes ready" {
		t.Errorf("nodes = %+v", f)
	}
	if f := got["dev/namespaces"]; f.Title != "dev: 1 namespace" {
		t.Errorf("namespaces = %+v", f)
	}

	k.Want = []string{"nodes"}
	facts, _ = k.Facts(context.Background())
	if len(facts) != 1 || facts[0].ID != "dev/nodes" {
		t.Fatalf("filtered facts = %+v", facts)
	}
}

func TestFluxFacts(t *testing.T) {
	dc := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds(),
		ks("apps", "True"), ks("infra", "True"), ks("broken", "False"),
	)
	f := &Flux{SourceName: "dev", Client: dc}
	facts, err := f.Facts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// no HelmReleases: no fact about them
	if len(facts) != 1 {
		t.Fatalf("facts = %+v", facts)
	}
	if facts[0].Title != "Flux Kustomizations: 2/3 ready" || facts[0].Message != "dev: 2 of 3 Flux Kustomizations ready, 1 not ready" {
		t.Errorf("fact = %+v", facts[0])
	}
}

func TestHarvesterFacts(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	dc := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds(),
		vm("web", "Running", now.Add(-48*time.Hour)),
		vm("db", "Stopped", now.Add(-400*24*time.Hour)),
		vm("ci", "Running", now.Add(-time.Hour)),
	)
	h := &Harvester{SourceName: "harvester", Client: dc, Now: func() time.Time { return now }}
	facts, err := h.Facts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := byID(facts)
	if f := got["harvester/vms"]; f.Title != "3 VMs on harvester" || f.Message != "harvester: 3 VMs, 2 of them running" {
		t.Errorf("vms = %+v", f)
	}
	if f := got["harvester/oldest-vm"]; f.Title != "Oldest VM: db" || !strings.Contains(f.Message, "400 days") {
		t.Errorf("oldest = %+v", f)
	}
}

func TestBuildSkipsDisabledAndBroken(t *testing.T) {
	off := false
	srcs, errs := Build([]Config{
		{Name: "off", Type: TypeKubernetes, Enabled: &off},
		{Name: "missing", Type: TypeFlux, Kubeconfig: "/does/not/exist"},
	}, time.Second)
	if len(srcs) != 0 || len(errs) != 1 {
		t.Fatalf("srcs = %v, errs = %v", srcs, errs)
	}
}

func byID(facts []Fact) map[string]Fact {
	m := map[string]Fact{}
	for _, f := range facts {
		m[f.ID] = f
	}
	return m
}

func pod(name string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}, Status: corev1.PodStatus{Phase: phase}}
}

func node(name string, ready corev1.ConditionStatus) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}},
	}
}

func listKinds() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		KustomizationGVR:  "KustomizationList",
		HelmReleaseGVR:    "HelmReleaseList",
		VirtualMachineGVR: "VirtualMachineList",
	}
}

func ks(name, ready string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kustomize.toolkit.fluxcd.io/v1",
		"kind":       "Kustomization",
		"metadata":   map[string]any{"name": name, "namespace": "flux-system"},
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": "Ready", "status": ready},
		}},
	}}
}

func vm(name, status string, created time.Time) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachine",
		"metadata":   map[string]any{"name": name, "namespace": "default"},
		"status":     map[string]any{"printableStatus": status},
	}}
	u.SetCreationTimestamp(metav1.NewTime(created))
	return u
}

// The example shipped in the repository must stay valid.
func TestExampleFile(t *testing.T) {
	if _, err := LoadConfig("../../examples/sources.yaml"); err != nil {
		t.Fatal(err)
	}
}
