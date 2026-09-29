# plugin-kube

The Kubernetes plugin for [opencharly/charly](https://github.com/opencharly/charly) —
it owns ALL Kubernetes cluster interaction, keeping the heavy
`k8s.io/client-go` + `k8s.io/apimachinery` stack out of charly's core `go.mod`.

The plugin is an out-of-tree Go module: charly's loader fetches this repo,
go-builds the provider binary on the HOST, and serves it **out-of-process** over
go-plugin gRPC via the charly plugin SDK.

## What it provides

| Capability | Surface |
|---|---|
| `verb:kube` | the `kube:` cluster-probe check verb — 13 methods |
| `deploy:kubernetes` | the `target: kubernetes` workload deploy substrate |
| `deploy:kindcluster` | the local Kubernetes-in-Docker cluster substrate |

## The verb

An authored `kube: <method>` step (scalar sugar) or
`kube: {method: …, cluster: …}` (map form) desugars to the plugin input
envelope; the method + every kube-exclusive modifier live in the plugin's own
`#KubeInput` (`schema/kube.cue`). Methods: `nodes`, `wait-nodes`, `pods`,
`wait-ready`, `ingress`, `ingressclass`, `storageclass`, `service`,
`lb-external-ip`, `addons`, `apply`, `delete`, `raw`.

```yaml
- check: the cluster reports at least one Ready node
  id: kube-nodes-ready
  kube:
      method: nodes
  stdout:
      - contains: Ready
  context: [deploy]
```

## The deploy substrates

- **`target: kubernetes`** — `kubectl apply -k` on the plugin-generated Kustomize
  tree. The plugin's own `preresolve.go` resolves the cluster template + image
  Capabilities and GENERATES the egress-validated Kustomize tree itself
  (reaching `verb:k8sgen`/`verb:egress` peer-to-peer), then applies it.
- **`target: kindcluster`** — a local Kubernetes-in-Docker cluster provisioned
  by the upstream `kind` tool on the operator's container engine, then the same
  Kustomize workload.
- **k3s post-provision finalization** — the guest-forward kubeconfig rewrite
  (this plugin's own `k3s_post.go`) + the `~/.kube/config` merge.

## How to use it

Compose the plugin candy in a box or check bed's `candy:` list:

```yaml
- '@github.com/opencharly/plugin-kube/candy/plugin-kube:<tag>'
```

## Layout

- `candy/plugin-kube/` — the plugin module: `plugin.go`, `provider.go`,
  `cluster.go`, `deploy.go`, `materialize.go`, `preresolve.go`, `merge.go`,
  `kindcluster.go`, `k3s_post.go`, `methods.go`, `schema/kube.cue`,
  `params/cue_types_gen.go`, `cmd/serve/main.go`.
- `candy/plugin-kube/charly.yml` — the `plugin-kube:` candy entity.
- `charly.yml` — the root project manifest (`discover: candy`).
- `.github/workflows/tag-on-merge.yml` — CalVer tag + `CHANGELOG/` on merge.

## Related

- Owning skill: `/charly-kubernetes:kubernetes` — the Kubernetes deploy surface,
  cluster profiles, and Kustomize manifest generation. This candy carries no
  `skill:` entity of its own; the gap is tracked in
  [opencharly/opencharly#291](https://github.com/opencharly/opencharly/issues/291).
- `/charly-kubernetes:check-k8s` — the `kube:` check verb reference.
- `/charly-internals:plugin` — the plugin/provider model.
- [`opencharly/charly`](https://github.com/opencharly/charly) — the charly CLI.
