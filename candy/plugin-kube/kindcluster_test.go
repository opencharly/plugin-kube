package kube

// kindcluster_test.go — unit coverage for the deploy:kindcluster provider's pure
// logic: the kind Cluster config render, the engine→provider mapping, the
// cluster-name sanitization, the workload-image walk, and the kindcluster→kubernetes
// policy projection the Kustomize generator consumes. The live create/apply path is
// carried by the check-kindcluster-* R10 beds (a real cluster is required, so it
// cannot be a unit test).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestKindProviderForEngine_MapsTheCapabilityData pins the documented kind edge:
// charly's engine WORD is containerd, kind has no containerd provider, and the
// capability data routes the word through its CLI (nerdctl). podman/docker are
// identity mappings. The mapping must come from spec.EngineBinaries, not a local
// literal, so this asserts the spec-derived answer.
func TestKindProviderForEngine_MapsTheCapabilityData(t *testing.T) {
	cases := map[string]string{
		"podman":     "podman",
		"docker":     "docker",
		"containerd": "nerdctl", // the ENGINE word is containerd; nerdctl is its CLI
	}
	for engine, want := range cases {
		got, err := kindProviderForEngine(engine)
		if err != nil {
			t.Errorf("kindProviderForEngine(%q): %v", engine, err)
			continue
		}
		if got != want {
			t.Errorf("kindProviderForEngine(%q) = %q, want %q", engine, got, want)
		}
	}
}

// TestKindProviderForEngine_RejectsUnsupportedWord is the R3/R7 arm for the silent
// fallback: container.EngineBinary resolves an unknown/empty word to
// spec.DefaultContainerEngine (podman), so the pre-change path provisioned a
// podman-backed cluster for `engine: containerd` under the old vocabulary — and
// would do the same for any typo. kindProviderForEngine must return an ERROR and
// name the supported set, never a provider.
//
// `nerdctl` is included deliberately: it is the DELETED engine word (spec#266), so
// it is exactly the word a deploy might still carry, and it must not silently run
// podman. This test fails against the pre-change resolution (which returned
// "podman", nil for every one of these).
func TestKindProviderForEngine_RejectsUnsupportedWord(t *testing.T) {
	for _, word := range []string{"bogus", "nerdctl", "Podman", ""} {
		provider, err := kindProviderForEngine(word)
		if err == nil {
			t.Fatalf("engine %q must be rejected, got provider %q (the silent podman fallback)", word, provider)
		}
		if provider != "" {
			t.Errorf("engine %q must not yield a provider on error, got %q", word, provider)
		}
		for _, supported := range spec.EngineNames {
			if !strings.Contains(err.Error(), supported) {
				t.Errorf("engine %q error must name the supported word %q: %v", word, supported, err)
			}
		}
	}
}

// TestKindClusterConfig_JSONKeysMatchEgress asserts the rendered config carries the
// JSON keys the egress gate sees (apiVersion/nodes/role) — NOT the Go field names.
// The preresolver round-trips the config through json.Marshal → map → the gate; a
// yaml-only struct tag yields APIVersion/Nodes and the gate rejects the config as
// missing apiVersion/nodes (measured live). This test fails on that regression.
func TestKindClusterConfig_JSONKeysMatchEgress(t *testing.T) {
	cfg := renderKindClusterConfig(&spec.Kindcluster{Nodes: []spec.KindclusterNode{{Role: "control-plane"}}}, kindDefaultNodeImage)
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["apiVersion"]; !ok {
		t.Fatalf("config JSON must carry apiVersion (egress key), got keys %v", keysOf(doc))
	}
	if _, ok := doc["APIVersion"]; ok {
		t.Fatalf("config JSON leaked the Go field name APIVersion: %v", keysOf(doc))
	}
	nodes, ok := doc["nodes"].([]any)
	if !ok || len(nodes) == 0 {
		t.Fatalf("config JSON must carry a non-empty nodes array, got %v", doc["nodes"])
	}
	n0, _ := nodes[0].(map[string]any)
	if _, ok := n0["role"]; !ok {
		t.Fatalf("node JSON must carry role, got %v", n0)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestRenderKindClusterConfig_DefaultSingleControlPlane(t *testing.T) {
	kc := &spec.Kindcluster{}
	cfg := renderKindClusterConfig(kc, kindDefaultNodeImage)
	if cfg.Kind != "Cluster" || cfg.APIVersion != "kind.x-k8s.io/v1alpha4" {
		t.Fatalf("unexpected envelope: %+v", cfg)
	}
	if len(cfg.Nodes) != 1 || cfg.Nodes[0].Role != "control-plane" {
		t.Fatalf("default topology must be a single control-plane, got %+v", cfg.Nodes)
	}
	if cfg.Nodes[0].Image != kindDefaultNodeImage {
		t.Errorf("default node must carry the digest-pinned image, got %q", cfg.Nodes[0].Image)
	}
}

func TestRenderKindClusterConfig_ExplicitTopologyAndPortMappings(t *testing.T) {
	kc := &spec.Kindcluster{
		Nodes: []spec.KindclusterNode{
			{Role: "control-plane", ExtraPortMappings: []spec.KindclusterPortMapping{
				{ContainerPort: 30080, HostPort: 30080, ListenAddress: "127.0.0.1", Protocol: "TCP"},
			}},
			{Role: "worker"},
			{Role: "worker", Image: "kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed"},
		},
	}
	cfg := renderKindClusterConfig(kc, kindDefaultNodeImage)
	if len(cfg.Nodes) != 3 {
		t.Fatalf("want 3 nodes, got %d", len(cfg.Nodes))
	}
	if cfg.Nodes[0].Role != "control-plane" || len(cfg.Nodes[0].ExtraPortMappings) != 1 {
		t.Errorf("control-plane port mapping not rendered: %+v", cfg.Nodes[0])
	}
	if cfg.Nodes[0].ExtraPortMappings[0].ContainerPort != 30080 || cfg.Nodes[0].ExtraPortMappings[0].HostPort != 30080 {
		t.Errorf("port mapping values wrong: %+v", cfg.Nodes[0].ExtraPortMappings[0])
	}
	// Per-node image override wins; others keep the default pin (never a floating tag).
	if cfg.Nodes[2].Image == kindDefaultNodeImage {
		t.Error("per-node image override ignored")
	}
	if cfg.Nodes[1].Image != kindDefaultNodeImage {
		t.Errorf("worker without an image must keep the pinned default, got %q", cfg.Nodes[1].Image)
	}
}

func TestKindClusterName_Sanitized(t *testing.T) {
	got := kindClusterName("check:kindcluster/docker")
	if got == "" || got == "check:kindcluster/docker" {
		t.Fatalf("cluster name must be sanitized, got %q", got)
	}
}

func TestKindclusterAsKubernetes_ProjectsPolicy(t *testing.T) {
	kc := &spec.Kindcluster{
		Box:               "",
		KubeconfigContext: "kind-lab",
		DefaultNamespace:  "apps",
		AdmissionPolicy:   "restricted",
		Storage:           &spec.KubernetesStorage{ClassDefault: "standard"},
		Ingress:           &spec.KubernetesIngressDefaults{Enabled: true, Class: "nginx"},
	}
	kub := kindclusterAsKubernetes(kc)
	if kub.KubeconfigContext != "kind-lab" || kub.DefaultNamespace != "apps" || kub.AdmissionPolicy != "restricted" {
		t.Errorf("identity fields not projected: %+v", kub)
	}
	if kub.Storage.ClassDefault != "standard" {
		t.Errorf("storage policy not projected: %+v", kub.Storage)
	}
	if !kub.Ingress.Enabled || kub.Ingress.Class != "nginx" {
		t.Errorf("ingress policy not projected: %+v", kub.Ingress)
	}
}

func TestFirstContainerImage_WalksAllWorkloadShapes(t *testing.T) {
	deploy := map[string]any{"spec": map[string]any{
		"template": map[string]any{"spec": map[string]any{
			"containers": []any{map[string]any{"image": "quay.io/x/app:1"}},
		}},
	}}
	if got := firstContainerImage(deploy); got != "quay.io/x/app:1" {
		t.Errorf("deployment walk: got %q", got)
	}
	pod := map[string]any{"spec": map[string]any{
		"containers": []any{map[string]any{"image": "quay.io/x/pod:2"}},
	}}
	if got := firstContainerImage(pod); got != "quay.io/x/pod:2" {
		t.Errorf("pod walk: got %q", got)
	}
	if got := firstContainerImage(map[string]any{}); got != "" {
		t.Errorf("empty doc must yield empty image, got %q", got)
	}
}
