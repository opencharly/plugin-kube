---
name: kubernetes
description: '`charly deploy add`, `charly deploy from-box`, Kustomize manifest generation,
  cluster profiles, Kubernetes deployments, `deploy:` block in deploy spec, or OCI-label
  capabilities.'
---

# Kubernetes — Deploying OpenCharly Images to Kubernetes Clusters

## Overview

OpenCharly can deploy built images to a Kubernetes cluster by emitting a Kustomize `base/` + `overlays/` tree. The deployment schema stays **target-agnostic** — the deploy node describes *what the workload needs* (kind, replica, resources, exposure, storage, probes); a per-cluster **`kind: kubernetes` cluster template** (the `kubernetes:` entity, which absorbed the former cluster-profile file) supplies the Kubernetes-specific knobs (storage class, ingress class, cert issuer, secret backend).

Every box runtime contract is baked into OCI labels at build time, so **a Kubernetes deploy is possible without access to `charly.yml`** — the `charly deploy from-box` verb reads capabilities from the pushed image alone.

A **local-cluster sibling** reuses this same Kustomize + `kubectl` machinery: `kindcluster` (`kind: kindcluster`) runs kind on the operator's engine and is served by the `deploy:kindcluster` provider beside `deploy:kubernetes` in `candy/plugin-kube`. See `/charly-kubernetes:kind`.

## Quick reference

| Action | Command | Description |
|---|---|---|
| Add Kubernetes deploy | `charly deploy add <name> <ref>` | Read BoxConfig + the deploy node + the `kind: kubernetes` cluster template; emit `.opencharly/k8s/<name>/` Kustomize tree |
| Source-less deploy | `charly deploy from-box <registry/name:tag> [name] --cluster <name>` | Deploy from OCI labels only — no `charly.yml` needed (see Part F.10) |
| Re-apply to cluster | `charly deploy add <name>` | Re-materialize the tree and `kubectl apply -k` it against the operator's kubeconfig — the apply IS the deploy (`candy/plugin-kube/deploy.go`) |
| Show generated manifests | `charly deploy show <name>` | `kubectl kustomize …` — see what would apply |
| Delete Kubernetes deploy | `charly deploy del <name>` | Remove overlay dir; base stays if other instances reference it |

## The three-layer model

| Concern | Schema slot | OCI label home |
|---|---|---|
| **Build** — what goes INTO the image | `box.build:` (or legacy `BoxConfig`) | no (consumed at build) |
| **Capabilities** — box runtime contract | `box.capabilities:` (or layer rollups) | **yes** — every field under `ai.opencharly.*` |
| **Deployment** — how to run the image | a name-first deploy node (substrate kind at the edge — here `kubernetes:`) in `charly.yml` + `~/.config/charly/charly.yml` overlay | no |

The completeness invariant: every exported field on `spec.BoxMetadata` (there is no more `Capabilities` alias — see `/charly-internals:capabilities`) has a `CapabilityLabelMap` entry. A compile-time test enforces this — a new capability field without a label mapping fails the build. See `sdk/deploykit/capabilities.go`.

## Deployment schema — target-agnostic fields

A Kubernetes deployment is a name-first `kubernetes:` deploy: the `kubernetes:` value carries the
scalars + the cross-refs (`image:` = the box to deploy, `from:` = the `kind: kubernetes`
cluster template) AND every non-scalar field (resources / security / expose /
storage / probes / the `deploy:` deploy-knobs block) — all inline in the `kubernetes:`
value.

```yaml
openclaw:
  kubernetes:
    image: openclaw                 # the box to deploy (falls back to the deploy name)
    from: production                # cross-ref → inherit the kind:kubernetes cluster template
    kind: service                   # service | daemon | batch | scheduled | oneshot
    replica: 3
    restart: always                 # always | on-failure | never (honored on Pod/Job/CronJob)
    resources:
      cpu_request: "500m"
      memory_request: 512Mi
    security:
      memory_max: 2Gi               # → resources.limits.memory
      cpus: "1.5"                   # → resources.limits.cpu
    expose:
      host: openclaw.example.com
      path: /
      tls: true                     # → cert-manager annotation from the cluster template
    storage:
      - {name: data, size: 20Gi, class_hint: fast, access: single-writer}
    probes:
      # Each probe is a bare #Op (spec/schema/deploy.cue #DeployProbes), and #Op is
      # CLOSED — there is no `http:` sugar inside probes:, because the plugin-verb
      # desugar runs on plan STEPS only. Author them as ops.
      liveness:  {command: curl -fsS http://localhost:8080/healthz}
      readiness: {command: curl -fsS http://localhost:8080/ready}
    deploy:
      namespace: apps               # optional override of the cluster template default
      patches: []                   # escape hatch: strategic / JSON6902 patches
      raw: []                       # escape hatch: paths to raw manifests included verbatim
```

**Workload kind heuristic** (`selectWorkloadKind`, inside `candy/plugin-k8sgen/k8sgen.go` — the compiled-in generator candy itself; reached from `candy/plugin-kube/materialize.go`'s `materializeKustomize` via `verb:k8sgen`/`OpEmit` peer dispatch, PLUGIN-SIDE now — the former core `charly/k8s_generate.go` is deleted, K5-A item 6):

```
kind: service   + storage: []       → Deployment
kind: service   + storage: [...]    → StatefulSet  (auto volumeClaimTemplates)
kind: daemon                        → DaemonSet
kind: batch                         → Job
kind: scheduled (+schedule:)        → CronJob
kind: oneshot                       → Pod
```

Explicit override: `deploy.workload: Deployment` (rare — prefer `kind:`).

## Cluster template (`kind: kubernetes`)

One `kind: kubernetes` entity per cluster, declared name-first in `charly.yml`. The `kubernetes:` kind **absorbed the former `kind: cluster-profile` file** — `charly migrate` synthesizes a `kind: kubernetes` entry from any pre-existing `clusters/<name>.yaml`. It is the **only** place cluster-specific Kubernetes knobs live; a deploy reaches it by name through its `from:` cross-ref. The `kubernetes:` value carries the scalars (`box:` cross-ref, `kubeconfig_context:`, `admission_policy:`, `default_namespace:`) AND every non-scalar policy block (storage / ingress / secret / image_default / pod_default / defaults) — all inline in the `kubernetes:` value.

```yaml
production:                          # a kind: kubernetes cluster template (name-first)
  kubernetes:
    box: ""                          # empty → a cluster-policy-only template (the workload box is named on the deploy)
    kubeconfig_context: gke_prod_us-east1
    admission_policy: restricted     # restricted | baseline | privileged
    default_namespace: apps
    storage:
      class_default: fast-ssd-retain
      class_fast: fast-ssd
      class_cheap: hdd-delete
      class_encrypted: fast-ssd-luks
      access_mode_default: ReadWriteOnce
    ingress:
      enabled: true
      class: nginx
      cert_issuer: letsencrypt-prod  # cert-manager ClusterIssuer
      path_type_default: Prefix
    secret:
      backend: external-secrets      # external-secrets | sealed-secrets | raw
      store: vault-prod
      prefix: prod/
    image_default:
      pull_policy: IfNotPresent
      pull_secrets: [regcred-prod]
    pod_default:
      priority_class: standard
      tolerations: []
      node_selector: {}
    defaults:
      labels: {managed-by: opencharly}
```

New cluster = write a new `kind: kubernetes` template; zero deploy changes.

## Generator output

```
.opencharly/k8s/<deployment-name>/
├── base/
│   ├── kustomization.yaml           # commonLabels, resources: […]
│   ├── deployment.yaml              # or statefulset.yaml / daemonset.yaml / job.yaml / cronjob.yaml / pod.yaml
│   ├── service.yaml
│   ├── pvc-<name>.yaml              # per storage entry (Deployment/DaemonSet); StatefulSet uses volumeClaimTemplates
│   ├── ingress.yaml                 # when expose.host set AND the kubernetes template's ingress.enabled
│   └── raw/                         # copied from deploy.raw:
└── overlays/
    └── <instance>/                  # "default" for bare name; "prod" for image/prod
        └── kustomization.yaml       # namespace override + patches from deploy.patches:
```

Apply: `kubectl apply -k .opencharly/k8s/<name>/overlays/<instance>` (or re-run `charly deploy add <name>`).

**Egress validation.** Every manifest is validated through `validateEgressValue`/`writeYAML`
(candy/plugin-kube/materialize.go) before it
is written: workload / service / pvc / ingress against the `#K8sObject` envelope
(non-empty `apiVersion`/`kind` + named `metadata`), and the base + overlay
`kustomization.yaml` against `#Kustomization`. A structurally-broken manifest fails
deploy generation instead of reaching the cluster. Owned by `/charly-internals:egress`.

## Source-less deploy — `charly deploy from-box`

Proves the self-contained image invariant: a deploy pipeline with **no access to `charly.yml`** can still produce a correct Kustomize tree.

```bash
# On a machine that doesn't have the source repo:
charly deploy from-box quay.io/myorg/openclaw:v2 openclaw \
    --cluster production --namespace apps
# Reads: OCI labels (capabilities) + the `production` kind:kubernetes cluster template
#        + ~/.config/charly/charly.yml (if present, for per-machine overrides)
# Emits: .opencharly/k8s/openclaw/base/ + overlays/default/
charly deploy add openclaw                   # kubectl apply -k ...
```

## Relevant code

- `spec/spec/cue_types_gen.go` (generated) — `KubernetesDeploy` (`KubernetesDeployConfig`, the deploy's `deploy:` deploy-knobs block: namespace / workload override / patches / raw; the former `charly/k8s_config.go` is DELETED, K-wave 2)
- `spec/spec/cue_types_gen.go` (generated) — `KubernetesSpec` (the `kind: kubernetes` cluster template; absorbed the former `ClusterProfile` / `clusters/*.yaml`)
- `candy/plugin-kube/preresolve.go` — the PLUGIN-side `deploy:kubernetes` preresolver (F6/FINAL-K5-unit-6a, relocated from the DELETED `charly/k8s_deploy_preresolve.go`). `target: kubernetes` is an EXTERNAL deploy substrate (F1): it resolves out-of-process to candy/plugin-kube's `deploy:kubernetes` provider (beside its `kube:` verb), NOT a compiled-in deploy target. The preresolver resolves the image Capabilities + the `kind: kubernetes` cluster template by self-loading the project PLUGIN-SIDE (`sdk/loaderkit.ResolveKubernetesEntityViaExecutor`, K-wave W3a A3-phase-2 — no HostBuild round trip remains for this lookup), runs `materializeKustomize` (`materialize.go`) — which InvokeProviders the compiled-in `verb:k8sgen`/`OpEmit` for the manifest docs and `verb:egress`/`OpValidate` to validate each one, entirely peer-to-peer, no host round trip — and ships the egress-validated overlay path in `DeployVenue.Substrate`; the plugin runs `kubectl --context <ctx> apply -k`. The user-facing surface (schema, cluster templates, Kustomize output) is unchanged. See `/charly-internals:install-plan` + `/charly-internals:plugin`.
- `charly/k8s_generate.go` / `host_build_k8s_generate.go` are DELETED — the former "k8s-generate-kustomize" HostBuild seam they served is retired (K5-A item 6): the Kustomize GENERATION now runs entirely inside `candy/plugin-kube/materialize.go` (see above), and `candy/plugin-fleet/deploy_from_box.go`'s `charly deploy from-box` path calls `materializeKustomize` the same way. The generator itself still lives in `candy/plugin-k8sgen/k8sgen.go`, reached over `verb:k8sgen` peer dispatch, not a core shim
- `candy/plugin-k8sgen/` — the compiled-in Kustomize GENERATOR candy (M13): `k8sgen.go` (the ported workload/service/pvc/ingress/kustomization builders + the workload-kind heuristic + `GenerateTree`), serving `verb:k8sgen`/`OpEmit` over `spec.KubernetesGenInput` → `spec.KubernetesGenReply` manifest docs; no client-go (split from the heavy external plugin-kube so it resolves in the project-less from-box path)
- `candy/plugin-fleet/deploy_from_box.go` — the plugin-side `charly deploy from-box` Kubernetes path (`--cluster`), calling `materializeKustomize` the same way (`charly/fleet_from_box_cmd.go` is DELETED, K-wave 2 cone R2 — the pod path lives in `candy/plugin-fleet/from_box_pod.go`)
- `sdk/deploykit/capabilities.go` — `CapabilityLabelMap` + completeness check, over `spec.BoxMetadata` (`charly/capabilities.go`, this row's former citation, no longer exists — see `/charly-internals:capabilities`)

## Related skills

- `/charly-core:deploy` — unified `charly deploy add`/`del` verb; Kubernetes is one of the five deploy substrates
- `/charly-internals:capabilities` — OCI label contract the Kubernetes generator reads from
- `/charly-internals:install-plan` — shared IR across build + deploy targets
- `/charly-internals:egress` — the CUE egress gate that validates every generated manifest before write (`#K8sObject` / `#Kustomization`)
