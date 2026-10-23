---
name: check-k8s
description: Kubernetes cluster-probe declarative check verb — the `kube:` check verb
  (nodes, pods, ingress, storage class, addon health, apply/delete, arbitrary resource
  GETs) served out-of-process by candy/plugin-kube (vendored client-go; no external
  kubectl).
---

MUST be invoked before any work involving: the declarative `kube:` check
verb, cluster-readiness probes from a candy/box plan, ingress / storage
class assertions, k3s default-addon health checks, or authoring `kube:`
steps in a candy/box plan (the candy's `plan:` list) in charly.yml.

**There is no host `charly check kube` command.** `kube` is a DECLARATIVE
check verb only: it is authored as a `kube: <method>` inline Op in a candy/box
plan `check:` step and dispatched through the provider registry to the
out-of-process `candy/plugin-kube` module — the same way the bed's checks run
under `charly check live` / `charly check run`. The Kubernetes
cluster-probe implementation (and the `k8s.io/client-go` +
`k8s.io/apimachinery` dependency) lives entirely in that external plugin, NOT
in charly's core. This mirrors the `adb:` and `appium:` verbs (see
charly/check_cmd.go).

The cluster-probe verb is spelled `kube`; the deploy KIND is spelled
`kubernetes` (`kind: kubernetes`, a `kubernetes:` entity or cross-ref).

The **local-cluster sibling** is `kindcluster` (`kind: kindcluster`, a
`kindcluster:` entity): kind provisions the cluster as node containers on the
operator's own engine (docker / rootless podman / rootless nerdctl) instead of
a VM. It is the SAME `kube:` probe surface — a `kindcluster:` deploy writes a
`kind-<cluster>` kubeconfig context, which a `kube:` step addresses with
`kube_context:`. See `/charly-kubernetes:kind`.

## Method surface

Every method below is the `kube:` map's `method:` (or the scalar value for a bare
method). The Modifiers column names the kube-exclusive fields that live INSIDE the
`kube:` map — EXCEPT `timeout:`, which is a shared `#Op` sibling of the `kube:` key.
A `kube:` step is a `check:` step.

| `kube:` value | Modifiers | Output |
|---|---|---|
| `nodes` | — | `<name> <Ready\|NotReady>` per line |
| `wait-nodes` | `kube_count:` (N), `name:` (host), `timeout:` (120s) | block until N (or the named) node is Ready |
| `pods` | `namespace:`, `label:` (selector) | `<ns>/<name> <phase>` per line |
| `wait-ready` | `kube_kind:` (K), `name:` (N), `namespace:`, `timeout:` (120s) | block until the resource is Ready |
| `ingress` | `namespace:` | `<ns>/<name> class=<c> hosts=<h> backends=<b>` |
| `ingressclass` | — | `<name> default=<bool>` |
| `storageclass` | — | `<name> default=<bool>` |
| `service` | `namespace:` | `<ns>/<name> <type> <clusterIP> <externalIP>` |
| `lb-external-ip` | `namespace:`, `name:` (svc), `timeout:` (60s) | print the assigned external IP |
| `addons` | `namespace:` (kube-system), `timeout:` (180s) | roll-up: Traefik + ServiceLB + local-path all Ready |
| `apply` | `manifest:` (path), `namespace:` | apply multi-doc YAML via the dynamic client |
| `delete` | `manifest:` (path), `namespace:` | delete the resources named in the manifest |
| `raw` | `kube_resource:` (plural), `kube_group:`, `kube_version:` (v1), `name:`, `namespace:` | GET an arbitrary resource as JSON |

The full kube-map field set: `name:`, `namespace:`, `label:`, `cluster:`,
`kubeconfig:`, `kube_context:`, `kube_kind:`, `kube_count:`, `manifest:`,
`kube_resource:`, `kube_group:`, `kube_version:`, `json:` (all inside the `kube:`
map). The only shared `#Op` sibling a `kube:` step commonly carries is `timeout:`.

## Cluster selection

Every method accepts the same three cluster-selection modifiers. The plugin
resolves the `cluster:` profile to a concrete kubeconfig context via the generic
`cc.ResolveClusterContext` reverse-leg; the precedence is:

1. `kubeconfig: <path>` — direct kubeconfig file pointer. Overrides
   everything.
2. `cluster: <name>` — a `kind: kubernetes` cluster template name. The
   plugin resolves it via `ResolveKubernetesEntityViaExecutor` (the
   project `charly.yml` loader) to the template's `kubeconfig_context:`,
   which selects the context; the kubeconfig path defaults to `$KUBECONFIG`
   then `~/.kube/config`.
3. `kube_context: <name>` — override the kubeconfig context directly.
4. None given → current-context of the default kubeconfig (matches
   `kubectl` with no flags).

`charly deploy add vm:k3s-srv` (or any deploy whose layers include
`k3s-server`) provisions a cluster whose kubeconfig is merged into the
default kubeconfig under a context named after the deploy (the plugin-side
`k3s-post-provision` finalization dispatched by `candy/plugin-fleet`'s
`k3sPostProvision` (InvokeProviding `verb:kube` peer-to-peer — the former
core `invokeKubePluginWithBroker` seam is deleted), which retrieves the
kubeconfig, rewrites its
guest-forwarded server, and merges it via `mergeKubeconfig` — all inside
`candy/plugin-kube`), so a plan step can then address it with `cluster: k3s-srv`:

```yaml
- check: every node reports Ready
  kube:
    method: nodes
    cluster: k3s-srv
  stdout: {contains: "Ready"}
  context: [deploy]
```

## Declarative `kube:` steps in a candy's plan

The verb is authored from a candy's plan steps via the `kube:`
discriminator on a step's `Op`. In the unified node-form the plan IS a `plan:`
list — each step is an ordered list item under the candy's `plan:` (named by its
optional `id:`). Every method above maps to a method name; its kube-exclusive
fields (`name:`, `namespace:`, `cluster:`, `kubeconfig:`, `kube_kind:`,
`kube_count:`, `manifest:`, `kube_resource:`, `kube_group:`, `kube_version:`) go
INSIDE the `kube:` map, while `timeout:` stays a sibling. A `kube:` step is a
`check:` step.

Example from the main repo's `charly.yml` — the `check-k3s-vm` bed's
cluster-readiness steps, each naming ITS OWN `kind: kubernetes` profile by literal:

```yaml
check-k3s-vm:
  vm:
    from: k3s-vm
    disposable: true
    plan:
      # … guest-side command / process / port / file steps elided …
      - check: kubernetes=wait-nodes
        id: kv-kubernetes-wait-nodes
        kube:
          method: wait-nodes
          cluster: "check-k3s-vm-ctx"    # this bed's OWN kind:kubernetes profile
          kube_count: 1
        timeout: 180s
        stdout: {contains: "Ready"}
        context: [runtime]
      # `addons` BLOCKS until Traefik + ServiceLB + local-path are all Ready, so it
      # MUST precede any ingressclass/storageclass step — those resources are
      # registered by the addon stack. Ordering matters: `ingressclass`/`storageclass`
      # are one-shot list verbs with no internal wait, and they exit 0 on an EMPTY
      # list, so a `contains` matcher run before the addons settle FAILS rather than
      # waits. Gate first, assert second.
      - check: kubernetes=addons
        id: kv-kubernetes-addons
        kube:
          method: addons
          cluster: "check-k3s-vm-ctx"
        timeout: 240s
        context: [runtime]
      - check: kubernetes=ingressclass
        id: kv-kubernetes-ingressclass-traefik
        kube:
          method: ingressclass
          cluster: "check-k3s-vm-ctx"
        stdout: {contains: "traefik"}
        context: [runtime]
```

**A `kube:` step belongs to whoever can NAME the cluster — which is the deploy, not
a generic candy.** The bed above names `check-k3s-vm-ctx`, a `kind: kubernetes` profile it
alone owns, pinned to its own per-deploy kubeconfig context. Its sibling bed
`check-k8s-deploy` names `check-k8s-deploy-cluster-ctx`, so the two never resolve
through each other's context even though both deploy the SAME shared `kind: vm`
entity.

**Do NOT write `cluster: "${DEPLOY_NAME}"`.** `${DEPLOY_NAME}` is a runtime-only
check var holding the sanitized name (`:`/`.`/`/` → `-`) of the deployment under
check, and it is not a cluster selector: for a VM live check it is seeded from the
`kind: vm` ENTITY name (`candy/plugin-check/live_gather.go`, `pluginCheckLiveVM`),
which every bed deploying that entity SHARES — so it addresses another deployment's
context, or none at all, and the step fails with `no kubeconfig context selected`.
Threading the profile through a deploy-set env var is worse, not better: an
unresolved check var is a SKIP rather than a failure (`sdk/kit/planrun.go`), and the
VM check-var environment is a fixed map carrying no arbitrary deploy env, so such a
step goes silently vacuous. A generic candy that must prove its own control plane
came up probes it IN-VENUE instead — `candy/k3s-server/charly.yml` drives the k3s
client entrypoint (`/usr/local/bin/kubectl`) against the server's local kubeconfig,
needing no host kubeconfig merge, no port-forward, and no `kind: kubernetes` entity.

(`${DEPLOY_NAME}` is UPPERCASE because the check-var expander only recognizes
uppercase names; a lowercase `${deploy_name}` — the artifact-path token — is NOT a
check var and is rejected by `charly box validate` in kube identifier fields.)

`wait-nodes` with `name:` set matches a single specific node — the shape a
multi-node deploy uses to confirm one named worker joined. Without `name:`, it
waits until `kube_count:` nodes are Ready.

## Method notes

- **apply / delete** — limited to the kinds in `kindToPluralResource()`
  (in the plugin's `cluster.go`). Static table by design; adding a new
  kind is a one-line addition, avoiding the RESTMapper discovery bloat.
  Documents without a namespace inherit `namespace:`.
- **raw** — escape hatch for any resource not covered by the named
  methods. `kube: {method: raw, kube_resource: nodes}` lists nodes;
  `kube: {method: raw, kube_resource: configmaps, namespace: kube-system,
  name: foo}` prints one ConfigMap as JSON.
- **addons** — assumes the stock k3s addon stack (Traefik, ServiceLB,
  local-path-provisioner) in `kube-system`. Explicit `disable:` in a
  k3s-server layer will cause this method to fail — the failure is
  intentional since the test speaks to "default k3s stack healthy".
- **lb-external-ip** — polls `.status.loadBalancer.ingress[].ip` /
  `[].hostname` until one appears; for k3s this is ServiceLB
  (klipper-lb) advertising the host's node IP.

## Implementation

The verb is dispatched out-of-process; the client-go stack does not link into
charly's core binary.

- `candy/plugin-kube` — the out-of-tree plugin module that owns the verb and
  the entire `k8s.io/client-go` + `k8s.io/apimachinery` dependency:
  - `provider.go` — the Provider that advertises the `kube` verb; the
    registry routes a `kube:` step to it (`ResolveVerb("kube")` → its
    `grpcProvider` → `invokeVerbProvider` hands it the full `#Op` as
    `params_json`).
  - `cluster.go` — builds the `rest.Config` from kubeconfig + context (the
    dynamic client via `k8s.io/client-go/dynamic` + `unstructured` walkers,
    no typed clientset) and `kindToPluralResource()` for apply/delete.
  - `methods.go` — the `dispatch()` method router + the 13 method
    implementations (`runNodes`, `runWaitNodes`, `runApply`, …).
  - `merge.go` — `mergeKubeconfig`: the clientcmd merge that folds a retrieved
    k3s kubeconfig into the operator's `~/.kube/config` under a named context
    (so the `k8s.io/client-go/tools/clientcmd` dependency lives here too, not
    in core). Called DIRECTLY by this same plugin's `k3s_post.go`
    (`k3sPostProvision`) — no separate host-orchestrated merge round-trip.
  - `k3s_post.go` — the WHOLE k3s post-provision finalization (S3, FINAL/K5
    unit 6, relocated wholesale from the former `charly/k3s_post.go`, itself
    now DELETED — see below): `k3sPostProvision` checks the retrieved-kubeconfig
    path, rewrites its GUEST-local server URL to the HOST-forwarded port
    (`deployVMForwards` resolves the deploy tree node + the `kind:vm` entity's
    declared `port_forwards` by self-loading the project PLUGIN-SIDE — K-wave
    W3a A3-phase-2, `sdk/loaderkit.ResolveMergedTreeViaExecutor` /
    `ResolveVmEntityViaExecutor` — no HostBuild round trip remains for this
    leg, then reads the PERSISTED port-forward allocation ledger via the
    PLUGIN-SIDE `hostConfigResolveVmState` → `sdk/loaderkit.ResolveVmStateViaExecutor`
    read (the config-resolve HostBuild seam is DELETED, K-wave 2 cone R2 bank D —
    the SAME read `candy/plugin-vm`'s own `hostConfigResolve` + `candy/plugin-deploy-vm`'s
    `resolvePriorVmState` use, R2 — a FIX-ROUND regression fix: a direct
    `deploykit.LoadDeployConfigForRead` call from this out-of-process plugin
    silently found nothing every time, since `deploykit.DeployStateHost` is
    wired only by charly-core's own `init()`), then calls `mergeKubeconfig`
    directly.
  - `schema/kube.cue` — the plugin's served CUE schema: the `#KubeInput` def
    carries the method enum + every kube modifier, served over the Describe
    channel and spliced onto the base for validation. Authoring is unchanged
    (`kube: nodes`, not `plugin: kube`); the internal plugin/plugin_input wire
    envelope the sugar desugars to is never authored.
- `charly/k8s_plugin.go` / `charly/k3s_post.go` / `charly/k8s_config.go` are
  ALL DELETED — the former core seam that built a synthetic `kube:` `#Op` and
  dispatched it to the plugin WITH the reverse-channel broker
  (`invokeKubePluginWithBroker`) is gone. `candy/plugin-fleet`'s own
  `k3sPostProvision` (`secrets_artifacts.go`) InvokeProviders `verb:kube`
  peer-to-peer directly (`exec.InvokeProvider(ctx, "verb", "kube", …)`, with
  an explicit `kit.ShellExecutor{}` venue override reproducing the original
  "broker only, no live venue" contract) to trigger the
  `k3s-post-provision` method. The retrieve-check, port-forward rewrite, and
  merge all run INSIDE `candy/plugin-kube` (see `k3s_post.go` above); the
  cluster-template lookup that used to need `charly/k8s_config.go`'s
  `findKubernetesSpec` now self-loads plugin-side too (`sdk/loaderkit.ResolveKubernetesEntityViaExecutor`,
  K-wave W3a A3-phase-2). No client-go import, and no `kube:`-specific host
  seam, remains in core.

There is no `charly/k8s_cmd.go`, `kubeMethods` table, `runKube` dispatcher,
`posKube*` flag builder, or `k8sClusterFlags`/`LoadClusterProfile` symbol —
all were removed when the verb was externalized.

## Related skills

- `/charly-check:check` — the unified `charly check` surface (image / live /
  run), the plan-step vocabulary, and how the provider registry dispatches
  declarative verbs.
- `/charly-kubernetes:kubernetes` — deploying images to a Kubernetes cluster
  (`kind: kubernetes` cluster templates, Kustomize generation, `charly deploy`).
- `/charly-internals:plugin` — the Provider model and the out-of-process
  plugin dispatch the `kube:` verb rides on.
- `/charly-infrastructure:k3s` — the k3s-server / k3s-agent candies whose
  plans author these `kube:` readiness steps.
