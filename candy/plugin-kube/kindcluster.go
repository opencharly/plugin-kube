package kube

// kindcluster.go — the `deploy:kindcluster` SUBSTRATE provider: a local
// Kubernetes-in-Docker cluster provisioned by the upstream `kind` tool
// (kubernetes-sigs/kind). It is the local-cluster sibling of deploy:kubernetes and
// reuses this SAME plugin's Kubernetes machinery wholesale (R3): the workload it
// optionally runs is the identical egress-validated Kustomize tree
// (materializeKustomize) applied with `kubectl apply -k`, and the same teardown
// shape (remove the generated tree).
//
// The DIFFERENCE from deploy:kubernetes is the cluster DELIVERY: where
// deploy:kubernetes assumes an already-reachable cluster addressed by
// kubeconfig_context (a k3s VM, a real cluster), deploy:kindcluster PROVISIONS the
// cluster on the operator's container engine. kind creates node containers on that
// engine, so:
//
//   - the engine is charly's own engine word (podman/docker/nerdctl), mapped 1:1 to
//     kind's KIND_EXPERIMENTAL_PROVIDER — no separate selector;
//   - the kubeconfig is written by kind into the operator's ~/.kube/config under the
//     context `kind-<name>` with the real API host port kind allocated, so there is
//     NO guest-forward rewrite (the k3s complication simply does not arise);
//   - teardown is `kind delete cluster` (idempotent on a missing cluster), not a
//     remote delete.
//
// The plugin runs as a HOST subprocess (LocalTransport), so it runs the host's
// `kind`/`kubectl` directly against the host engine — no reverse channel needed
// beyond the template/node lookup.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/container"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// deployKindclusterVersion is the candy version stamped onto the ledger record
// (kept in lockstep with charly.yml + the Describe capability version).
const deployKindclusterVersion = "2026.174.1200"

// kindDefaultNodeImage is the fallback node image when neither the deploy nor the
// template pins one. Digest-pinned for reproducibility (kind's own guidance); it
// tracks the kind release pinned by the layer-kind candy.
const kindDefaultNodeImage = "kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5"

// kindClusterConfigWait bounds `kind create cluster --wait`. A cold cluster pulls
// the node image on first use; 5m covers a slow first pull and still fails rather
// than hanging forever.
const kindClusterConfigWait = "5m"

// kindPreresolveParams decodes the host's marshalDeployOpParams envelope (the SAME
// ad-hoc shape the deploy:kubernetes preresolver carries).
type kindPreresolveParams struct {
	Name string       `json:"name"`
	Dir  string       `json:"dir"`
	Node *spec.Deploy `json:"node"`
}

// kindClusterConfig is the rendered kind Cluster config (cluster-api kind.x-k8s.io).
// Egress-validated against egress_kind.cue before it is written.
type kindClusterConfig struct {
	Kind       string           `yaml:"kind"`
	APIVersion string           `yaml:"apiVersion"`
	Nodes      []kindConfigNode `yaml:"nodes"`
}

type kindConfigNode struct {
	Role              string            `yaml:"role"`
	Image             string            `yaml:"image,omitempty"`
	ExtraPortMappings []kindPortMapping `yaml:"extraPortMappings,omitempty"`
}

type kindPortMapping struct {
	ContainerPort int    `yaml:"containerPort"`
	HostPort      int    `yaml:"hostPort"`
	ListenAddress string `yaml:"listenAddress,omitempty"`
	Protocol      string `yaml:"protocol,omitempty"`
}

// invokeKindclusterPreresolve serves Invoke(OpPreresolve) for deploy:kindcluster.
// It resolves the kindcluster template + image capabilities, renders + egress-
// validates the kind Cluster config, materializes the optional workload Kustomize
// tree, and returns a spec.KindclusterDeployVenue.
func invokeKindclusterPreresolve(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("deploy:kindcluster preresolve: reach host reverse channel: %w", err)
	}
	var p kindPreresolveParams
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &p); err != nil {
			return nil, fmt.Errorf("deploy:kindcluster preresolve: decode params: %w", err)
		}
	}

	node := p.Node
	if node == nil {
		tree, terr := loaderkit.ResolveMergedTreeViaExecutor(ctx, exec, p.Dir)
		if terr != nil {
			return nil, fmt.Errorf("deploy:kindcluster preresolve: resolve deploy tree: %w", terr)
		}
		n, ok := tree[p.Name]
		if !ok {
			return nil, fmt.Errorf("deploy:kindcluster preresolve: resolve deploy %q: no deploy entry %q", p.Name, p.Name)
		}
		node = &n
	}

	clusterName := ""
	if node != nil {
		clusterName = node.From
	}
	if clusterName == "" {
		return nil, fmt.Errorf("deploy %q: target=kindcluster requires `kindcluster:` (kind:kindcluster cluster reference) on the deployment entry", p.Name)
	}

	// Resolve the kindcluster template body PLUGIN-SIDE using only exported SDK +
	// spec API (the generic kind-blind ProjectTemplates lookup), so no per-kind sdk
	// resolver leg is needed (R3): the template body is the same opaque RawBody the
	// kernel folds, decoded here into the concrete spec.Kindcluster.
	kc, err := resolveKindclusterTemplate(ctx, exec, p.Dir, clusterName)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: resolving kindcluster %q: %w", p.Name, clusterName, err)
	}

	// The engine: the deploy's own `engine:`, else the template's, else the resolved
	// runtime engine. It maps 1:1 to kind's KIND_EXPERIMENTAL_PROVIDER.
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return nil, fmt.Errorf("deploy %q: resolving runtime: %w", p.Name, err)
	}
	engine := string(rt.RunEngine)
	if node != nil && node.Engine != "" {
		engine = string(node.Engine)
	} else if kc.Engine != "" {
		engine = string(kc.Engine)
	}
	provider := container.EngineBinary(engine)

	nodeImage := kindDefaultNodeImage
	if kc.NodeImage != "" {
		nodeImage = kc.NodeImage
	}

	// Render the kind Cluster config from the template topology.
	cfg := renderKindClusterConfig(kc, nodeImage)
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: marshal kind config: %w", p.Name, err)
	}
	var cfgDoc any
	if err := json.Unmarshal(cfgJSON, &cfgDoc); err != nil {
		return nil, fmt.Errorf("deploy %q: decode kind config: %w", p.Name, err)
	}
	// Egress-validate the rendered config BEFORE it is written (the config charly
	// writes onto the host is gated, per /charly-internals:egress).
	if err := validateEgressValue(ctx, exec, "kind_cluster", "kindcluster.config", cfgDoc); err != nil {
		return nil, fmt.Errorf("deploy %q: %w", p.Name, err)
	}
	cfgYAML, err := yaml.Marshal(cfgDoc)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: render kind config yaml: %w", p.Name, err)
	}

	sanitized := kindClusterName(p.Name)
	kubeContext := kc.KubeconfigContext
	if kubeContext == "" {
		kubeContext = "kind-" + sanitized
	}

	venue := spec.KindclusterDeployVenue{
		ClusterName:   sanitized,
		Provider:      provider,
		NodeImage:     nodeImage,
		ClusterConfig: cfgYAML,
		KubeContext:   kubeContext,
		DeployName:    p.Name,
	}

	// Optional workload: only when the deploy names a runnable image (mirrors
	// deploy:kubernetes' requirement). The cluster-only case (no image) is legal —
	// the deploy just provisions the cluster.
	if node != nil && node.Image != "" {
		imageRef, capsJSON, rerr := resolveKindWorkloadImage(node, p.Name, rt.RunEngine)
		if rerr != nil {
			return nil, rerr
		}
		// Project the kindcluster cluster-policy fields onto a #Kubernetes body: the
		// Kustomize generator consumes the SAME policy surface (R3), so the kindcluster
		// cluster reuses the kubernetes generator verbatim.
		kub := kindclusterAsKubernetes(kc)
		kubJSON, merr := json.Marshal(kub)
		if merr != nil {
			return nil, fmt.Errorf("deploy %q: marshal projected cluster: %w", p.Name, merr)
		}
		genReply, gerr := materializeKustomize(ctx, exec, spec.KubernetesGenerateKustomizeRequest{
			Name:        p.Name,
			ImageRef:    imageRef,
			Node:        node,
			CapsJSON:    capsJSON,
			ClusterJSON: kubJSON,
		})
		if gerr != nil {
			return nil, fmt.Errorf("deploy %q: generating kustomize: %w", p.Name, gerr)
		}
		venue.OverlayPath = genReply.OverlayPath
		venue.TreeRoot = filepath.Clean(genReply.TreeRoot)
	}

	out, err := json.Marshal(venue)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: marshal kindcluster venue: %w", p.Name, err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

// resolveKindclusterTemplate loads the project plugin-side and returns the decoded
// kindcluster template named name. Uses the generic exported API (LoadUnifiedViaExecutor
// + ProjectTemplates().ByKind) — the SAME kind-blind lookup
// loaderkit.resolveKindTemplateBodyViaExecutor performs, without a per-kind sdk leg.
func resolveKindclusterTemplate(ctx context.Context, exec *sdk.Executor, dir, name string) (*spec.Kindcluster, error) {
	uf, ok, err := loaderkit.LoadUnifiedViaExecutor(ctx, exec, dir)
	if err != nil {
		return nil, err
	}
	if !ok || uf == nil {
		return nil, fmt.Errorf("no charly.yml or no kind:kindcluster entities declared")
	}
	templates := uf.ProjectTemplates()
	if templates == nil {
		return nil, fmt.Errorf("no templates declared")
	}
	body := templates.ByKind("kindcluster")[name]
	if len(body) == 0 {
		return nil, fmt.Errorf("kind:kindcluster entity %q not found", name)
	}
	var kc spec.Kindcluster
	if err := json.Unmarshal(body, &kc); err != nil {
		return nil, fmt.Errorf("decode kind:kindcluster entity %q: %w", name, err)
	}
	return &kc, nil
}

// kindclusterAsKubernetes projects a kindcluster cluster template onto the #Kubernetes
// shape the Kustomize generator consumes — one policy surface for both cluster kinds
// (R3). Only the shared policy + identity fields carry; kindcluster's kind-specific
// topology (nodes/node_image/engine) is not part of the workload generation. The
// kindcluster schema models the policy sub-blocks as optional (pointers); the
// #Kubernetes shape models them as values, so each is dereferenced here.
func kindclusterAsKubernetes(kc *spec.Kindcluster) spec.Kubernetes {
	kub := spec.Kubernetes{
		Box:               kc.Box,
		KubeconfigContext: kc.KubeconfigContext,
		DefaultNamespace:  kc.DefaultNamespace,
		AdmissionPolicy:   kc.AdmissionPolicy,
		Defaults:          kc.Defaults,
	}
	if kc.Storage != nil {
		kub.Storage = *kc.Storage
	}
	if kc.Ingress != nil {
		kub.Ingress = *kc.Ingress
	}
	if kc.ImageDefault != nil {
		kub.ImageDefault = *kc.ImageDefault
	}
	if kc.PodDefault != nil {
		kub.PodDefault = *kc.PodDefault
	}
	return kub
}

// resolveKindWorkloadImage resolves the image ref + capabilities for a kindcluster
// deploy that runs a workload — the SAME resolution deploy:kubernetes performs
// (preresolve.go), factored here for the kindcluster leg.
func resolveKindWorkloadImage(node *spec.Deploy, name, engine string) (imageRef string, capsJSON []byte, err error) {
	authored := node.Image
	if authored == "" {
		authored = name
	}
	if node.Version != "" {
		imageRef = spec.LeafName(authored) + ":" + node.Version
		if !kit.LocalImageExists(engine, imageRef) {
			return "", nil, fmt.Errorf("deploy %q: pinned image %q not present in local %s storage", name, imageRef, engine)
		}
	} else {
		resolved, rerr := kit.ResolveLocalImageRef(engine, spec.LeafName(authored))
		if rerr != nil {
			return "", nil, fmt.Errorf("deploy %q: resolving image %q: %w", name, authored, rerr)
		}
		imageRef = resolved
	}
	caps, cerr := deploykit.ExtractMetadata(engine, imageRef)
	if cerr != nil {
		return "", nil, fmt.Errorf("deploy %q: extracting capabilities from image %q: %w", name, imageRef, cerr)
	}
	if caps == nil {
		return "", nil, fmt.Errorf("deploy %q: image %q has no ai.opencharly labels (not an opencharly image?)", name, imageRef)
	}
	capsJSON, merr := json.Marshal(caps)
	if merr != nil {
		return "", nil, fmt.Errorf("deploy %q: marshal capabilities: %w", name, merr)
	}
	return imageRef, capsJSON, nil
}

// renderKindClusterConfig renders the kind Cluster config from the template topology.
// A single control-plane node is the kind default; explicit nodes (roles + per-node
// images/port mappings) override it.
func renderKindClusterConfig(kc *spec.Kindcluster, defaultImage string) kindClusterConfig {
	cfg := kindClusterConfig{Kind: "Cluster", APIVersion: "kind.x-k8s.io/v1alpha4"}
	if len(kc.Nodes) == 0 {
		cfg.Nodes = []kindConfigNode{{Role: "control-plane", Image: defaultImage}}
		return cfg
	}
	for _, n := range kc.Nodes {
		cn := kindConfigNode{Role: string(n.Role)}
		// Every node carries the digest-pinned image so kind never falls back to a
		// floating default (reproducibility). A per-node image overrides.
		cn.Image = defaultImage
		if n.Image != "" {
			cn.Image = n.Image
		}
		for _, pm := range n.ExtraPortMappings {
			cn.ExtraPortMappings = append(cn.ExtraPortMappings, kindPortMapping{
				ContainerPort: pm.ContainerPort,
				HostPort:      pm.HostPort,
				ListenAddress: pm.ListenAddress,
				Protocol:      pm.Protocol,
			})
		}
		cfg.Nodes = append(cfg.Nodes, cn)
	}
	return cfg
}

// kindClusterName sanitizes a deploy name into a kind cluster name (kind requires a
// DNS-label-ish name). It mirrors kit.SanitizeDeployName's contract.
func kindClusterName(name string) string {
	return kit.SanitizeDeployName(name)
}

// invokeDeployKindcluster handles an OpExecute Invoke for the deploy:kindcluster
// substrate. It decodes the host-preresolved venue, provisions the cluster with
// `kind create cluster`, optionally loads the workload image + applies the Kustomize
// tree, and returns the teardown op.
func invokeDeployKindcluster(req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	venue, err := sdk.DecodeDeployVenue(req.GetEnvJson())
	if err != nil {
		return nil, fmt.Errorf("deploy:kindcluster: decode venue: %w", err)
	}
	if len(venue.Substrate) == 0 {
		return nil, fmt.Errorf("deploy:kindcluster: empty substrate payload (the preresolver produced no KindclusterDeployVenue)")
	}
	var kv spec.KindclusterDeployVenue
	if err := json.Unmarshal(venue.Substrate, &kv); err != nil {
		return nil, fmt.Errorf("deploy:kindcluster: decode kindcluster venue: %w", err)
	}
	if kv.ClusterName == "" {
		return nil, fmt.Errorf("deploy:kindcluster: venue carries no cluster name")
	}
	if kv.Provider == "" {
		return nil, fmt.Errorf("deploy:kindcluster: venue carries no engine provider")
	}

	// Render the egress-validated cluster config to a temp file for `kind create`.
	cfgFile, err := os.CreateTemp("", "charly-kind-"+kv.ClusterName+"-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("deploy:kindcluster: create config file: %w", err)
	}
	defer os.Remove(cfgFile.Name())
	if _, werr := cfgFile.Write(kv.ClusterConfig); werr != nil {
		cfgFile.Close()
		return nil, fmt.Errorf("deploy:kindcluster: write config: %w", werr)
	}
	if cerr := cfgFile.Close(); cerr != nil {
		return nil, fmt.Errorf("deploy:kindcluster: close config: %w", cerr)
	}

	// Provision the cluster on the operator's engine. Idempotent-friendly: a cluster
	// that already exists is left running (kind's `create` would fail; so probe first).
	if !kindClusterExists(kv.ClusterName) {
		if out, cerr := runKind(kv.Provider, "create", "cluster",
			"--name", kv.ClusterName,
			"--config", cfgFile.Name(),
			"--wait", kindClusterConfigWait); cerr != nil {
			return nil, fmt.Errorf("deploy:kindcluster: create cluster %q: %w\n%s", kv.ClusterName, cerr, strings.TrimSpace(out))
		}
	}

	// Optional workload: load the image into the node(s) + apply the Kustomize tree.
	if kv.OverlayPath != "" {
		if nodeImage := kindVenueWorkloadImage(kv.OverlayPath); nodeImage != "" {
			if lerr := kindLoadImage(kv.Provider, kv.ClusterName, nodeImage); lerr != nil {
				return nil, lerr
			}
		}
		ctxArgs := kubectlContextArgs(kv.KubeContext)
		hasHelm, helmNamespaces, herr := overlayHelmInfo(kv.OverlayPath)
		if herr != nil {
			return nil, fmt.Errorf("deploy:kindcluster: read overlay kustomization: %w", herr)
		}
		if out, aerr := applyOverlay(ctxArgs, kv.OverlayPath, hasHelm, helmNamespaces); aerr != nil {
			return nil, fmt.Errorf("deploy:kindcluster: apply overlay %s: %w\n%s", kv.OverlayPath, aerr, strings.TrimSpace(out))
		}
	}

	// Teardown: `kind delete cluster` (idempotent on a missing cluster) + remove the
	// generated tree. Recorded + replayed at `charly deploy del`.
	teardown := fmt.Sprintf("kind delete cluster --name %s >/dev/null 2>&1 || true;",
		shellSingleQuote(kv.ClusterName))
	if kv.TreeRoot != "" {
		teardown += fmt.Sprintf(" rm -rf %s", shellSingleQuote(kv.TreeRoot))
	}
	reverseOps := []spec.ReverseOp{sdk.PluginScriptReverseOp(spec.ScopeUser, teardown)}
	return sdk.BuildDeployReply(reverseOps, "plugin-kube", deployKindclusterVersion)
}

// kindClusterExists reports whether a kind cluster of this name exists on the
// engine. kind get clusters is ENGINE-SCOPED, so the provider env is set.
func kindClusterExists(name string) bool {
	out, err := runKind("", "get", "clusters")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

// runKind runs the host `kind` with KIND_EXPERIMENTAL_PROVIDER set to provider
// (empty = leave kind's own autodetect).
func runKind(provider string, args ...string) (string, error) {
	cmd := exec.Command("kind", args...)
	if provider != "" {
		cmd.Env = append(os.Environ(), "KIND_EXPERIMENTAL_PROVIDER="+provider)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// kindVenueWorkloadImage reads the workload image ref the generated overlay targets
// (the deployment.yaml container image), so it can be loaded into the kind node(s)
// before the apply. Empty when the overlay carries no image (nothing to load).
func kindVenueWorkloadImage(overlayPath string) string {
	// The overlay's kustomization references the base; read base/deployment.yaml.
	base := filepath.Join(filepath.Dir(filepath.Dir(overlayPath)), "base")
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(base, e.Name()))
		if rerr != nil {
			continue
		}
		var doc map[string]any
		if yaml.Unmarshal(raw, &doc) != nil {
			continue
		}
		if img := firstContainerImage(doc); img != "" {
			return img
		}
	}
	return ""
}

// firstContainerImage walks a workload manifest for the first container image.
func firstContainerImage(doc map[string]any) string {
	spec, ok := doc["spec"].(map[string]any)
	if !ok {
		return ""
	}
	// Deployment/StatefulSet/DaemonSet/Job: spec.template.spec.containers. Pod: spec.containers.
	if tmpl, ok := spec["template"].(map[string]any); ok {
		if ts, ok := tmpl["spec"].(map[string]any); ok {
			return firstImageFromContainers(ts)
		}
	}
	return firstImageFromContainers(spec)
}

func firstImageFromContainers(spec map[string]any) string {
	containers, ok := spec["containers"].([]any)
	if !ok || len(containers) == 0 {
		return ""
	}
	c, ok := containers[0].(map[string]any)
	if !ok {
		return ""
	}
	img, _ := c["image"].(string)
	return img
}

// kindLoadImage loads a locally-built image into the kind node(s). kind's
// `load docker-image` is BROKEN on podman (measured: "image not present locally"
// despite the image existing); `load image-archive` works on every engine, so this
// saves the image to a tar and loads the archive — the engine-agnostic path.
func kindLoadImage(provider, cluster, imageRef string) error {
	// Skip when the node already has it (idempotent re-run).
	if kindNodeHasImage(provider, cluster, imageRef) {
		return nil
	}
	engine := exec.Command(engineCLI(provider), "save", "-o", "-", imageRef)
	tarBytes, serr := engine.Output()
	if serr != nil {
		return fmt.Errorf("deploy:kindcluster: save image %q: %w", imageRef, serr)
	}
	archive, err := os.CreateTemp("", "charly-kind-image-*.tar")
	if err != nil {
		return fmt.Errorf("deploy:kindcluster: create image archive: %w", err)
	}
	defer os.Remove(archive.Name())
	if _, werr := archive.Write(tarBytes); werr != nil {
		archive.Close()
		return fmt.Errorf("deploy:kindcluster: write image archive: %w", werr)
	}
	if cerr := archive.Close(); cerr != nil {
		return fmt.Errorf("deploy:kindcluster: close image archive: %w", cerr)
	}
	if out, lerr := runKind(provider, "load", "image-archive", archive.Name(), "--name", cluster); lerr != nil {
		return fmt.Errorf("deploy:kindcluster: load image %q into %q: %w\n%s", imageRef, cluster, lerr, strings.TrimSpace(out))
	}
	return nil
}

// kindNodeHasImage probes whether the cluster's control-plane node already carries
// the image, via the engine's exec. Best-effort: a probe failure means "load it".
func kindNodeHasImage(provider, cluster, imageRef string) bool {
	node := cluster + "-control-plane"
	cmd := exec.Command(engineCLI(provider), "exec", node, "crictl", "images")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// crictl prints `repo tag imageID size`; match on the repo:tag without a digest.
	base := imageRef
	if i := strings.Index(base, "@"); i >= 0 {
		base = base[:i]
	}
	repo, tag := base, "latest"
	if i := strings.LastIndex(base, ":"); i >= 0 {
		repo, tag = base[:i], base[i+1:]
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == repo && fields[1] == tag {
			return true
		}
	}
	return false
}

// engineCLI maps a kind provider word to the container-engine CLI binary. It is the
// same mapping charly's engine class uses (container.EngineBinary), inlined here to
// avoid importing the class into a hot path that only needs the CLI name.
func engineCLI(provider string) string {
	if provider == "" {
		return "docker"
	}
	return provider
}
