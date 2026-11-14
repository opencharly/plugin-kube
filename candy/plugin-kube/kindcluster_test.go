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
	cfg := renderKindClusterConfig(&kindclusterSpec{Nodes: []kindclusterNodeView{{Role: "control-plane"}}}, kindDefaultNodeImage)
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
	kc := &kindclusterSpec{}
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
	kc := &kindclusterSpec{
		Nodes: []kindclusterNodeView{
			{Role: "control-plane", ExtraPortMappings: []kindPortMappingView{
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
	kc := &kindclusterSpec{
		KubeconfigContext: "kind-lab",
		DefaultNamespace:  "apps",
		AdmissionPolicy:   "restricted",
		Storage:           kubernetesStorageView{ClassDefault: "standard"},
		Ingress:           kubernetesIngressView{Enabled: true, Class: "nginx"},
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

// TestKindclusterAsKubernetes_JSONShape pins the projection's WIRE, byte for byte. The bytes
// below were captured from the implementation this change replaces (the contract's
// spec.Kubernetes marshalled with the same input), so this test fails if the plugin's own view
// ever emits a different request body to the Kustomize generator — the one property that
// makes "the body moved, the behaviour did not" checkable rather than asserted.
func TestKindclusterAsKubernetes_JSONShape(t *testing.T) {
	kc := &kindclusterSpec{
		KubeconfigContext: "kind-lab",
		DefaultNamespace:  "apps",
		AdmissionPolicy:   "restricted",
		Storage:           kubernetesStorageView{ClassDefault: "standard"},
		Ingress:           kubernetesIngressView{Enabled: true, Class: "nginx"},
	}
	got, err := json.Marshal(kindclusterAsKubernetes(kc))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"box":"","kubeconfig_context":"kind-lab","admission_policy":"restricted","default_namespace":"apps","storage":{"class_default":"standard"},"ingress":{"enabled":true,"class":"nginx"},"image_default":{},"pod_default":{},"kubevirt":{},"defaults":{}}`
	if string(got) != want {
		t.Fatalf("projection wire changed:\n got %s\nwant %s", got, want)
	}
}

// TestKindclusterSpec_DecodesAuthoredBody is the test the resolver's path needs and did not
// have: it decodes a REPRESENTATIVE kindcluster body — every field the plugin reads populated —
// through the same json.Unmarshal the resolver performs, and asserts each value lands on its
// field. A tag that diverges from the authored schema (nodes/node_image/engine, the policy
// sub-blocks) drops data silently, so this is what makes "read the body through the plugin's
// own view" checkable. It then proves the decoded view DRIVES the two consumers: the kind
// config renderer and the Kustomize projection.
func TestKindclusterSpec_DecodesAuthoredBody(t *testing.T) {
	body := []byte(`{
	  "box": "lab",
	  "kubeconfig_context": "kind-lab",
	  "default_namespace": "apps",
	  "admission_policy": "restricted",
	  "defaults": {"labels": {"team": "infra"}, "annotations": {"note": "x"}},
	  "storage": {"class_default": "standard", "class_fast": "fast", "class_cheap": "cheap", "class_encrypted": "enc", "access_mode_default": "ReadWriteOnce"},
	  "ingress": {"enabled": true, "class": "nginx", "cert_issuer": "letsencrypt", "path_type_default": "Prefix"},
	  "image_default": {"pull_policy": "Always", "pull_secrets": ["regcred"]},
	  "pod_default": {"priority_class": "high", "node_selector": {"pool": "ci"}, "tolerations": [{"key": "dedicated", "operator": "Exists"}]},
	  "engine": "docker",
	  "node_image": "kindest/node:v1.36.4@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	  "nodes": [
	    {"role": "control-plane", "extra_port_mappings": [{"container_port": 30080, "host_port": 30080, "listen_address": "127.0.0.1", "protocol": "TCP"}]},
	    {"role": "worker", "image": "kindest/node:v1.36.4@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	  ]
	}`)

	var kc kindclusterSpec
	if err := json.Unmarshal(body, &kc); err != nil {
		t.Fatalf("decode kindcluster body: %v", err)
	}

	// identity + policy sub-blocks
	if kc.Box != "lab" || kc.KubeconfigContext != "kind-lab" || kc.DefaultNamespace != "apps" || kc.AdmissionPolicy != "restricted" {
		t.Fatalf("identity fields not decoded: %+v", kc)
	}
	if kc.Defaults.Labels["team"] != "infra" || kc.Defaults.Annotations["note"] != "x" {
		t.Errorf("defaults not decoded: %+v", kc.Defaults)
	}
	if kc.Storage.ClassDefault != "standard" || kc.Storage.ClassFast != "fast" || kc.Storage.ClassCheap != "cheap" || kc.Storage.ClassEncrypted != "enc" || kc.Storage.AccessModeDefault != "ReadWriteOnce" {
		t.Errorf("storage not decoded: %+v", kc.Storage)
	}
	if !kc.Ingress.Enabled || kc.Ingress.Class != "nginx" || kc.Ingress.CertIssuer != "letsencrypt" || kc.Ingress.PathTypeDefault != "Prefix" {
		t.Errorf("ingress not decoded: %+v", kc.Ingress)
	}
	if kc.ImageDefault.PullPolicy != "Always" || len(kc.ImageDefault.PullSecrets) != 1 || kc.ImageDefault.PullSecrets[0] != "regcred" {
		t.Errorf("image_default not decoded: %+v", kc.ImageDefault)
	}
	if kc.PodDefault.PriorityClass != "high" || kc.PodDefault.NodeSelector["pool"] != "ci" || len(kc.PodDefault.Tolerations) != 1 {
		t.Errorf("pod_default not decoded: %+v", kc.PodDefault)
	}

	// the kind topology — what the renderer reads
	if kc.Engine != "docker" {
		t.Errorf("engine = %q, want docker", kc.Engine)
	}
	if kc.NodeImage != "kindest/node:v1.36.4@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("node_image not decoded: %q", kc.NodeImage)
	}
	if len(kc.Nodes) != 2 || kc.Nodes[0].Role != "control-plane" || kc.Nodes[1].Role != "worker" {
		t.Fatalf("nodes not decoded: %+v", kc.Nodes)
	}
	if len(kc.Nodes[0].ExtraPortMappings) != 1 {
		t.Fatalf("port mappings not decoded: %+v", kc.Nodes[0])
	}
	pm := kc.Nodes[0].ExtraPortMappings[0]
	if pm.ContainerPort != 30080 || pm.HostPort != 30080 || pm.ListenAddress != "127.0.0.1" || pm.Protocol != "TCP" {
		t.Errorf("port mapping fields wrong: %+v", pm)
	}
	if kc.Nodes[1].Image == "" {
		t.Error("per-node image not decoded")
	}

	// the decoded view DRIVES the renderer: two nodes, the control-plane's mapping, the
	// worker's own image, and the control-plane carrying the cluster-level node_image.
	// The real caller resolves the cluster-level default first (kc.NodeImage when set, else
	// the pinned fallback); mirror that here so the decoded node_image is the default.
	clusterImage := kc.NodeImage
	if clusterImage == "" {
		clusterImage = kindDefaultNodeImage
	}
	cfg := renderKindClusterConfig(&kc, clusterImage)
	if len(cfg.Nodes) != 2 {
		t.Fatalf("renderer ignored the decoded topology: %+v", cfg.Nodes)
	}
	if len(cfg.Nodes[0].ExtraPortMappings) != 1 || cfg.Nodes[0].ExtraPortMappings[0].HostPort != 30080 {
		t.Errorf("renderer lost the decoded port mapping: %+v", cfg.Nodes[0])
	}
	if cfg.Nodes[1].Image == kc.NodeImage {
		t.Error("renderer ignored the decoded per-node image override")
	}
	if cfg.Nodes[0].Image != kc.NodeImage {
		t.Errorf("control-plane must carry the decoded node_image, got %q", cfg.Nodes[0].Image)
	}

	// and it DRIVES the projection: the decoded policy is what the generator sees.
	kub := kindclusterAsKubernetes(&kc)
	if kub.KubeconfigContext != "kind-lab" || kub.DefaultNamespace != "apps" || kub.Storage.ClassFast != "fast" || !kub.Ingress.Enabled || kub.Ingress.CertIssuer != "letsencrypt" {
		t.Errorf("projection lost the decoded policy: %+v", kub)
	}
}
