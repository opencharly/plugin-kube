package kube

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/plugin-kube/candy/plugin-kube/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// provider.go is the out-of-process provider for ALL of plugin-kube's capabilities.
// Invoke branches on the request class: a "deploy" op drives the `deploy:kubernetes`
// SUBSTRATE (deploy.go — `kubectl apply -k` on the plugin-generated Kustomize tree (materialize.go's materializeKustomize, on the host));
// every other op is the `kube:` check VERB. For the verb, charly's host dispatches a
// `kube:` check step through the registry (ResolveVerb("kube") → this grpcProvider →
// Provider.Invoke) with the FULL #Op marshaled as params_json and a CheckEnv snapshot
// as env; the kube-exclusive fields ride the desugared plugin input (params.KubeInput —
// the per-verb fields left core #Op in the schema-compaction cutover). The SAME
// provider also serves the k3s post-provision finalization the deploy seam needs: that
// caller (candy/plugin-fleet/secrets_artifacts.go's k3sPostProvision) builds a synthetic op ({method:
// k3s-post-provision, artifact_key, deploy_name} in the input map) WITH a
// reverse-channel broker and reads the result's Message. Because the out-of-process
// verb path does NOT run the host-side matcher
// pipeline, this Invoke OWNS the whole verdict:
// dispatch the method, then evaluate the stdout/stderr/exit_status matchers itself
// (via the shared sdk implementation — R3), and return the wire {status,message}
// the host decodes.

// kubeEnv is the plugin-side decode of the CheckEnv the host ships as
// Operation.Env for a `kube:` check step (provider_checkenv.go) — only Mode/Box
// matter here (kube probes a cluster, not a container, so it needs no container
// resolution). The k3s-post-provision deploy seam ships no env (the plugin reads the
// artifact key / deploy name off the plugin input and uses os.UserHomeDir itself).
type kubeEnv struct {
	Box  string `json:"box"`
	Mode string `json:"mode"` // "live" | "box"
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one operation for the plugin's capabilities. The plugin serves BOTH
// the `kube:` check verb AND the `deploy:kubernetes` SUBSTRATE (F1), distinguished by the
// request's class: a "deploy" op runs `kubectl apply -k` against the plugin-generated
// Kustomize tree (deploy.go); every other op is the `kube:` verb. It decodes the
// full #Op + the env, handles the k3s-post-provision deploy seam first, skips in box
// mode (cluster probes need a reachable cluster, never a disposable `charly check
// box`), dispatches the method, and self-evaluates the matchers.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetClass() == "deploy" {
		// The deploy substrate word rides Reserved (kubernetes | kindcluster).
		if req.GetReserved() == "kindcluster" {
			switch req.GetOp() {
			case sdk.OpPreresolve:
				return invokeKindclusterPreresolve(ctx, req)
			}
			return invokeDeployKindcluster(req)
		}
		switch req.GetOp() {
		case sdk.OpPreresolve:
			return invokeKubernetesPreresolve(ctx, req)
		case sdk.OpEmit:
			// K5-A item 6: the from-box source-less path's entry point into the SAME
			// generate+write+validate logic OpPreresolve below uses (materialize.go, R3).
			return invokeKubernetesMaterialize(ctx, req)
		}
		return invokeDeployKubernetes(req)
	}
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "kube: decode op: "+err.Error())
		}
	}
	var env kubeEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	// The verb's method + kube-exclusive fields ride the desugared plugin input
	// since the schema-compaction cutover (the host preresolver writes the resolved
	// kube_context into the SAME input map).
	var in params.KubeInput
	kit.DecodeInput(op.PluginInput, &in)
	method := in.Method

	// k3s-post-provision is the k3s deploy seam (S3, FINAL/K5 unit 6 — relocated
	// wholesale from charly/k3s_post.go): retrieve-path check, guest-forward kubeconfig
	// rewrite, and the kubeconfig merge. Dispatched WITH a reverse-channel broker (the
	// caller — candy/plugin-fleet's k3sPostProvision — uses exec.InvokeProvider, mirroring the
	// deploy:kubernetes preresolve leg) because the guest-forward rewrite self-loads the project
	// (loaderkit.ResolveMergedTreeViaExecutor / ResolveVmEntityViaExecutor, K-wave W3a A3-phase-2)
	// and needs the reverse channel for that.
	if method == "k3s-post-provision" {
		exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
		if err != nil {
			return sdk.ResultJSON("fail", "kube: k3s-post-provision: reach host reverse channel: "+err.Error())
		}
		msg, err := k3sPostProvision(ctx, exec, k3sPostProvisionParams{ArtifactKey: in.ArtifactKey, DeployName: in.DeployName, VmEntity: in.VmEntity})
		if err != nil {
			return sdk.ResultJSON("fail", "kube: k3s-post-provision: "+err.Error())
		}
		return sdk.ResultJSON("pass", msg)
	}

	// Cluster-probe verb: skip under `charly check box` — there is no cluster to
	// reach on a disposable `podman run --rm` (mirrors the host's RunModeBox/box-mode skip).
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("kube: %s requires a running cluster (skip under charly check box)", method))
	}

	// Resolve the `cluster: <profile>` convenience to a concrete kubeconfig context —
	// PLUGIN-SIDE self-load now (K-wave W3a A3-phase-2: loaderkit.ResolveKubernetesEntityViaExecutor,
	// unblocked by W1's LoadUnifiedViaExecutor; the former "deploy-entity-resolve" HostBuild seam
	// this round-tripped through is deleted). This call carries no deploy name of its own (a
	// `kube:` check verb runs independent of any specific deploy), so it resolves the project dir
	// via hostProjectDir's os.Getwd()-on-the-host leg ("deploy-plugins-connect", Path="" — the
	// returned Dir is unconditional, only the plugin-connect side effect needs a real deploy name,
	// which this call doesn't need).
	//
	// A resolve FAILURE for a NAMED cluster is LOUD: the author set `cluster:`, so a resolve error
	// must not silently degrade to the kubeconfig current-context — on a host whose current-context
	// is empty that produced the bare `no kubeconfig context selected` and hid the cause (the
	// plugin-kubevirt twin, plugin-kubevirt#9). A legitimate miss still falls back: only a
	// hostProjectDir / resolve ERROR fails, and a resolved-but-empty context is left for the
	// current-context fallback below.
	if in.Cluster != "" && in.KubeContext == "" {
		exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
		if err != nil {
			return sdk.ResultJSON("fail", fmt.Sprintf("kube: %s: %v", method, err))
		}
		view, rerr := resolveVerbCluster(ctx, exec, in.Cluster)
		if rerr != nil {
			return sdk.ResultJSON("fail", fmt.Sprintf("kube: %s: %v (refusing to fall back to the kubeconfig current-context)", method, rerr))
		}
		applyResolvedCluster(&in, view)
	}

	conn := connFromInput(&in)
	out, runErr := dispatch(conn, &op, &in)

	// The shared exit/stdout/stderr verdict pipeline (R3). kube produces no artifact.
	return sdk.VerbVerdict("kube", method, out, runErr, &op, false)
}

// applyResolvedCluster merges a resolved cluster template into the step's connection inputs.
// Both halves are taken, each only when the template carries one: the CONTEXT selects inside the
// kubeconfig, and the FILE says which kubeconfig — an externally-managed cluster is addressed by a
// per-cluster file that the ambient $KUBECONFIG / ~/.kube/config never names (opencharly/spec#224,
// opencharly/plugin-kube#23). An explicitly authored `kubeconfig:` on the step always wins; a
// legitimate miss (nil view) leaves the caller's own fallback untouched.
func applyResolvedCluster(in *params.KubeInput, view *spec.ResolvedKubernetes) {
	if view == nil {
		return
	}
	if view.KubeconfigContext != "" {
		in.KubeContext = view.KubeconfigContext
	}
	if view.Kubeconfig != "" && in.Kubeconfig == "" {
		in.Kubeconfig = view.Kubeconfig
	}
}

// resolveVerbCluster is this plugin's ONE cluster resolution, delegated to its home in the sdk
// (loaderkit.ResolveClusterViaExecutor, opencharly/sdk#376). The local copy that used to live here
// returned the context ONLY, so it could not carry a template's kubeconfig file; it was the second
// occurrence of the mechanism the sdk hoisted (opencharly/sdk#338), and this change deletes it
// rather than teaching it the new field (R2/R5). The package var is the test seam, the pattern the
// k3s_post resolvers use.
var resolveVerbCluster = loaderkit.ResolveClusterViaExecutor
