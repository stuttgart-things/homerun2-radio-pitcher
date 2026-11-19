package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Kinds of rollout.
const (
	KindDeployment  = "deployment"
	KindStatefulSet = "statefulset"
	KindDaemonSet   = "daemonset"
	KindHelmRelease = "helmrelease"
)

// Rollout is a workload or Helm release that changed. Every field comes
// from the cluster.
type Rollout struct {
	// ID is stable per rollout (one revision of one object), so a rollout
	// airs once.
	ID        string
	Source    string
	Kind      string
	Namespace string
	Name      string
	// Version is the image tag of the first container, or a Helm release's
	// chart version; empty when there is none.
	Version string
	// Upgrade is false for a first install (Helm revision 1).
	Upgrade bool
	At      time.Time
}

// RolloutSource lists the rollouts of a cluster since a time.
type RolloutSource interface {
	Name() string
	Rollouts(ctx context.Context, since time.Time) ([]Rollout, error)
}

// Rollouts lists Deployments (their newest ReplicaSet), StatefulSets and
// DaemonSets (their newest ControllerRevision) whose current revision was
// created after since.
func (k *Kubernetes) Rollouts(ctx context.Context, since time.Time) ([]Rollout, error) {
	var out []Rollout

	rss, err := k.Client.AppsV1().ReplicaSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list replicasets: %w", err)
	}
	type newest struct {
		rev int64
		rs  *appsv1.ReplicaSet
	}
	byDeploy := map[string]newest{}
	for i := range rss.Items {
		rs := &rss.Items[i]
		owner := metav1.GetControllerOf(rs)
		if owner == nil || owner.Kind != "Deployment" {
			continue
		}
		rev, _ := strconv.ParseInt(rs.Annotations["deployment.kubernetes.io/revision"], 10, 64)
		key := rs.Namespace + "/" + owner.Name
		if cur, ok := byDeploy[key]; !ok || rev > cur.rev {
			byDeploy[key] = newest{rev, rs}
		}
	}
	for _, n := range byDeploy {
		rs := n.rs
		if !rs.CreationTimestamp.After(since) {
			continue
		}
		owner := metav1.GetControllerOf(rs)
		out = append(out, Rollout{
			ID:     k.SourceName + "/" + KindDeployment + "/" + rs.Namespace + "/" + rs.Name,
			Source: k.SourceName, Kind: KindDeployment, Namespace: rs.Namespace, Name: owner.Name,
			Version: firstImageTag(rs.Spec.Template.Spec.Containers),
			Upgrade: n.rev > 1,
			At:      rs.CreationTimestamp.Time,
		})
	}

	crs, err := k.Client.AppsV1().ControllerRevisions("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list controllerrevisions: %w", err)
	}
	type newestCR struct{ cr *appsv1.ControllerRevision }
	byOwner := map[string]newestCR{}
	for i := range crs.Items {
		cr := &crs.Items[i]
		owner := metav1.GetControllerOf(cr)
		if owner == nil || (owner.Kind != "StatefulSet" && owner.Kind != "DaemonSet") {
			continue
		}
		key := owner.Kind + "/" + cr.Namespace + "/" + owner.Name
		if cur, ok := byOwner[key]; !ok || cr.Revision > cur.cr.Revision {
			byOwner[key] = newestCR{cr}
		}
	}
	for _, n := range byOwner {
		cr := n.cr
		if !cr.CreationTimestamp.After(since) {
			continue
		}
		owner := metav1.GetControllerOf(cr)
		out = append(out, Rollout{
			ID:     k.SourceName + "/" + strings.ToLower(owner.Kind) + "/" + cr.Namespace + "/" + cr.Name,
			Source: k.SourceName, Kind: strings.ToLower(owner.Kind), Namespace: cr.Namespace, Name: owner.Name,
			Version: revisionImageTag(cr.Data.Raw),
			Upgrade: cr.Revision > 1,
			At:      cr.CreationTimestamp.Time,
		})
	}
	return out, nil
}

// Rollouts lists HelmReleases whose current Helm revision (status.history,
// newest first) was first deployed after since.
func (f *Flux) Rollouts(ctx context.Context, since time.Time) ([]Rollout, error) {
	list, err := f.Client.Resource(HelmReleaseGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list helmreleases: %w", err)
	}
	var out []Rollout
	for _, o := range list.Items {
		r, ok := helmRollout(f.SourceName, o)
		if ok && r.At.After(since) {
			out = append(out, r)
		}
	}
	return out, nil
}

func helmRollout(source string, o unstructured.Unstructured) (Rollout, bool) {
	hist, _, _ := unstructured.NestedSlice(o.Object, "status", "history")
	if len(hist) == 0 {
		return Rollout{}, false
	}
	h, ok := hist[0].(map[string]any)
	if !ok || h["status"] != "deployed" {
		return Rollout{}, false
	}
	at, err := time.Parse(time.RFC3339, fmt.Sprint(h["firstDeployed"]))
	if err != nil {
		return Rollout{}, false
	}
	rev, _ := strconv.ParseInt(fmt.Sprint(h["version"]), 10, 64)
	version, _ := h["chartVersion"].(string)
	return Rollout{
		ID:     fmt.Sprintf("%s/%s/%s/%s/%d", source, KindHelmRelease, o.GetNamespace(), o.GetName(), rev),
		Source: source, Kind: KindHelmRelease, Namespace: o.GetNamespace(), Name: o.GetName(),
		Version: version,
		Upgrade: rev > 1,
		At:      at,
	}, true
}

// firstImageTag is the tag of the first container's image.
func firstImageTag(cs []corev1.Container) string {
	if len(cs) == 0 {
		return ""
	}
	return ImageTag(cs[0].Image)
}

// revisionImageTag reads the first container's image from a
// ControllerRevision's data (a patch of the pod template).
func revisionImageTag(raw []byte) string {
	var d struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Image string `json:"image"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if json.Unmarshal(raw, &d) != nil || len(d.Spec.Template.Spec.Containers) == 0 {
		return ""
	}
	return ImageTag(d.Spec.Template.Spec.Containers[0].Image)
}

// ImageTag is the tag of an image reference: "v0.14.0" for
// "ghcr.io/x/y:v0.14.0@sha256:…", the short digest for "x@sha256:…", empty
// for an image without either.
func ImageTag(image string) string {
	ref, digest, _ := strings.Cut(image, "@")
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[i+1:]
	}
	if d := strings.TrimPrefix(digest, "sha256:"); d != "" {
		return "sha256:" + d[:min(7, len(d))]
	}
	return ""
}
