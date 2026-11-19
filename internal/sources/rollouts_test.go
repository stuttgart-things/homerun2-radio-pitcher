package sources

import (
	"context"
	"sort"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

var rolloutNow = time.Date(2026, 11, 19, 14, 0, 0, 0, time.UTC)

func owned(kind, name string) []metav1.OwnerReference {
	yes := true
	return []metav1.OwnerReference{{Kind: kind, Name: name, Controller: &yes}}
}

func rs(name, deploy, rev, image string, age time.Duration) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "homerun2", OwnerReferences: owned("Deployment", deploy),
			Annotations:       map[string]string{"deployment.kubernetes.io/revision": rev},
			CreationTimestamp: metav1.NewTime(rolloutNow.Add(-age)),
		},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Image: image}},
		}}},
	}
}

func cr(name, kind, owner string, rev int64, image string, age time.Duration) *appsv1.ControllerRevision {
	return &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "redis", OwnerReferences: owned(kind, owner),
			CreationTimestamp: metav1.NewTime(rolloutNow.Add(-age)),
		},
		Revision: rev,
		Data:     runtime.RawExtension{Raw: []byte(`{"spec":{"template":{"spec":{"containers":[{"image":"` + image + `"}]}}}}`)},
	}
}

func TestKubernetesRollouts(t *testing.T) {
	cs := fake.NewClientset(
		// radio: revision 3 is new, 2 is old
		rs("radio-a", "radio", "2", "ghcr.io/x/radio:v0.13.0", 30*time.Hour),
		rs("radio-b", "radio", "3", "ghcr.io/x/radio:v0.14.0@sha256:abcdef0123", time.Hour),
		// scout: its newest revision is old
		rs("scout-a", "scout", "1", "ghcr.io/x/scout:v1.0.0", 72*time.Hour),
		// a brand-new deployment
		rs("ui-a", "ui", "1", "ghcr.io/x/ui@sha256:0123456789", 2*time.Hour),
		cr("redis-1", "StatefulSet", "redis", 1, "redis:7.2", 100*time.Hour),
		cr("redis-2", "StatefulSet", "redis", 2, "redis:7.4", 3*time.Hour),
	)
	k := &Kubernetes{SourceName: "dev", Client: cs}
	got, err := k.Rollouts(context.Background(), rolloutNow.Add(-6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Name < got[j].Name })
	want := []Rollout{
		{ID: "dev/deployment/homerun2/radio-b", Source: "dev", Kind: KindDeployment, Namespace: "homerun2", Name: "radio", Version: "v0.14.0", Upgrade: true},
		{ID: "dev/statefulset/redis/redis-2", Source: "dev", Kind: KindStatefulSet, Namespace: "redis", Name: "redis", Version: "7.4", Upgrade: true},
		{ID: "dev/deployment/homerun2/ui-a", Source: "dev", Kind: KindDeployment, Namespace: "homerun2", Name: "ui", Version: "sha256:0123456", Upgrade: false},
	}
	if len(got) != len(want) {
		t.Fatalf("rollouts = %+v", got)
	}
	for i := range want {
		g := got[i]
		g.At = time.Time{}
		if g != want[i] {
			t.Errorf("rollout %d:\n got %+v\nwant %+v", i, g, want[i])
		}
	}
}

func hr(name string, history ...map[string]any) *unstructured.Unstructured {
	h := make([]any, len(history))
	for i := range history {
		h[i] = history[i]
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "helm.toolkit.fluxcd.io/v2",
		"kind":       "HelmRelease",
		"metadata":   map[string]any{"name": name, "namespace": "flux-system"},
		"status":     map[string]any{"history": h},
	}}
}

func TestFluxRollouts(t *testing.T) {
	at := func(age time.Duration) string { return rolloutNow.Add(-age).Format(time.RFC3339) }
	dc := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds(),
		hr("cert-manager",
			map[string]any{"version": int64(4), "status": "deployed", "chartVersion": "v1.15.0", "firstDeployed": at(time.Hour)},
			map[string]any{"version": int64(3), "status": "superseded", "chartVersion": "v1.14.0", "firstDeployed": at(50 * time.Hour)}),
		hr("old", map[string]any{"version": int64(2), "status": "deployed", "chartVersion": "1.0.0", "firstDeployed": at(30 * time.Hour)}),
		hr("failed", map[string]any{"version": int64(5), "status": "failed", "chartVersion": "2.0.0", "firstDeployed": at(time.Hour)}),
		hr("new", map[string]any{"version": int64(1), "status": "deployed", "chartVersion": "0.1.0", "firstDeployed": at(2 * time.Hour)}),
		hr("nohistory"),
	)
	f := &Flux{SourceName: "dev", Client: dc}
	got, err := f.Rollouts(context.Background(), rolloutNow.Add(-6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Name < got[j].Name })
	if len(got) != 2 {
		t.Fatalf("rollouts = %+v", got)
	}
	if g := got[0]; g.Name != "cert-manager" || g.Version != "v1.15.0" || !g.Upgrade || g.ID != "dev/helmrelease/flux-system/cert-manager/4" {
		t.Errorf("cert-manager = %+v", g)
	}
	if g := got[1]; g.Name != "new" || g.Upgrade {
		t.Errorf("new = %+v", g)
	}
}

func TestImageTag(t *testing.T) {
	for in, want := range map[string]string{
		"ghcr.io/x/radio:v0.14.0":                 "v0.14.0",
		"ghcr.io/x/radio:v0.14.0@sha256:abcdef01": "v0.14.0",
		"localhost:5000/x/radio":                  "",
		"localhost:5000/x/radio:1.2":              "1.2",
		"ghcr.io/x/radio@sha256:abcdef0123":       "sha256:abcdef0",
		"redis":                                   "",
	} {
		if got := ImageTag(in); got != want {
			t.Errorf("ImageTag(%q) = %q, want %q", in, got, want)
		}
	}
}
