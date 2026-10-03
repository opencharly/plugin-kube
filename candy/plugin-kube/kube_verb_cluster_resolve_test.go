package kube

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/spec"
)

// kube_verb_cluster_resolve_test.go pins the loud-resolution contract for opencharly/plugin-kube#15's
// `kube:` VERB half (the k3s-post half was fixed in #16): a `kube:` step that NAMES a cluster whose
// resolve FAILS must surface an error, never silently degrade to the kubeconfig current-context
// (which produced the bare "no kubeconfig context selected").

// stubResolveKubernetesEntity swaps the package-var seam for the duration of a test.
func stubResolveKubernetesEntity(t *testing.T, vm *spec.ResolvedKubernetes, err error) {
	t.Helper()
	orig := resolveKubernetesEntity
	resolveKubernetesEntity = func(ctx context.Context, ex *sdk.Executor, dir, name string) (*spec.ResolvedKubernetes, error) {
		return vm, err
	}
	t.Cleanup(func() { resolveKubernetesEntity = orig })
}

// TestResolveKubeVerbCluster_NilExecutor_ErrorsLoudly pins the no-executor path.
func TestResolveKubeVerbCluster_NilExecutor_ErrorsLoudly(t *testing.T) {
	kctx, err := resolveKubeVerbCluster(context.Background(), nil, "check-k3s-vm-ctx")
	if err == nil {
		t.Fatal("want an error for a nil executor, got nil")
	}
	if !strings.Contains(err.Error(), "check-k3s-vm-ctx") {
		t.Fatalf("error must name the cluster, got: %v", err)
	}
	if kctx != "" {
		t.Fatalf("want empty context on failure, got %q", kctx)
	}
}

// TestResolveKubeVerbCluster_ProjectDirResolveFails_ErrorsLoudly pins the deploy-plugins-connect
// leg failure: it must error naming the cluster, not degrade to "".
func TestResolveKubeVerbCluster_ProjectDirResolveFails_ErrorsLoudly(t *testing.T) {
	exec := sdk.NewInProcExecutor(&fakeExecutorServiceClient{connectErr: errors.New("host seam down")})
	_, err := resolveKubeVerbCluster(context.Background(), exec, "check-k3s-vm-ctx")
	if err == nil {
		t.Fatal("want an error when the project-dir resolve fails, got nil (the silent-swallow class)")
	}
	if !strings.Contains(err.Error(), "check-k3s-vm-ctx") {
		t.Fatalf("error must name the cluster, got: %v", err)
	}
}

// TestResolveKubeVerbCluster_EntityResolveFails_ErrorsLoudly pins the entity-resolve failure.
func TestResolveKubeVerbCluster_EntityResolveFails_ErrorsLoudly(t *testing.T) {
	stubResolveKubernetesEntity(t, nil, errors.New("resolve boom"))
	exec := sdk.NewInProcExecutor(&fakeExecutorServiceClient{projectDir: "/proj"})
	_, err := resolveKubeVerbCluster(context.Background(), exec, "check-k3s-vm-ctx")
	if err == nil {
		t.Fatal("want an error when the entity resolve fails, got nil")
	}
	if !strings.Contains(err.Error(), "check-k3s-vm-ctx") {
		t.Fatalf("error must name the cluster, got: %v", err)
	}
}

// TestResolveKubeVerbCluster_ResolvedButEmptyContext_FallsBack proves the legitimate-miss case
// still returns ("", nil) so the caller keeps its current-context fallback — the change must NOT
// turn an empty resolved context into a hard failure.
func TestResolveKubeVerbCluster_ResolvedButEmptyContext_FallsBack(t *testing.T) {
	stubResolveKubernetesEntity(t, &spec.ResolvedKubernetes{}, nil)
	exec := sdk.NewInProcExecutor(&fakeExecutorServiceClient{projectDir: "/proj"})
	kctx, err := resolveKubeVerbCluster(context.Background(), exec, "check-k3s-vm-ctx")
	if err != nil {
		t.Fatalf("a resolved-but-empty context must fall back, got error: %v", err)
	}
	if kctx != "" {
		t.Fatalf("want empty context for the fallback, got %q", kctx)
	}
}
