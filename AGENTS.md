# AGENTS.md — plugin-kube

Standalone out-of-tree plugin repo owning ALL Kubernetes cluster interaction
(`verb:kube` + `deploy:kubernetes` + `deploy:kindcluster`). The plugin is a Go
module at `candy/plugin-kube/` (module path
`github.com/opencharly/plugin-kube/candy/plugin-kube`); the root `charly.yml`
only declares `discover: candy` so the repo is a project and its candy is
scanned.

Canonical files:

- `candy/plugin-kube/charly.yml` — the `plugin-kube:` candy entity (`plugin:`
  block, `plan:` check).
- `candy/plugin-kube/` — the Go source: `plugin.go`, `provider.go`, `cluster.go`,
  `deploy.go`, `materialize.go`, `preresolve.go`, `merge.go`, `kindcluster.go`,
  `k3s_post.go`, `methods.go`, `schema/kube.cue`, `params/cue_types_gen.go`,
  `cmd/serve/main.go`.
- `.github/workflows/tag-on-merge.yml` — CalVer tag + `CHANGELOG/` on merge.
- `README.md` — user overview only; never agent guidance.

## Load these skills first (R0)

- `/charly-kubernetes:kubernetes` — the Kubernetes deploy surface, cluster
  profiles, and Kustomize manifest generation. Load before changing a substrate
  or the deploy path.
- `/charly-kubernetes:check-k8s` — the `kube:` cluster-probe check verb
  reference.
- `/charly-internals:plugin` — the plugin authoring reference: the `plugin:`
  block, the unified Provider model, the per-plugin CUE-schema contract.
- `/charly-internals:git-workflow` — before any git/PR action.

## Build / validate / test

- `go build ./...` in `candy/plugin-kube/` — compile the plugin module.
- `go test ./...` in `candy/plugin-kube/` — the plugin's Go tests.
- `charly box validate` at the repo root — the structural check (the candy +
  `plugin:` block, CUE schema).
- The merge gate is the **org-wide** `charly/pr-validator` (required check
  `validate / validate`, defined in `opencharly/.github`); this repo has **no**
  per-repo candy gate.
- The R10 consumers are the `check-k3s` bed (the `kube:` verb), the
  `check-k8s-deploy` bed (the `deploy:kubernetes` substrate), and the
  `check-kindcluster-*` beds (the `deploy:kindcluster` substrate).

## Modify this repo

- Edit the `plugin-kube:` candy entity, the Go source, and `schema/kube.cue`
  **together** — the schema is the single source for the verb's `params/` struct.
- Keep the `k8s.io/client-go`/`apimachinery` dependency HERE (the plugin exists to
  keep it out of charly core). The generated Kustomize tree is produced
  peer-to-peer (`verb:k8sgen`/`verb:egress`) with no host round trip.
- Keep the k3s post-provision finalization in this plugin's own `k3s_post.go`.

## Landing

- The authoritative rulebook is the umbrella `AGENTS.md` in
  `opencharly/opencharly` and `charly/AGENTS.md` in the charly repo. Load
  `/charly-internals:git-workflow` before any git/PR action; history lives in
  `CHANGELOG/`. Do not restate its rules here.
