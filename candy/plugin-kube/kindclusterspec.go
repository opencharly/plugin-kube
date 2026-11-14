package kube

// kindclusterspec.go — this plugin's OWN view of the `kindcluster:` cluster template, and of
// the `kubernetes:` policy body it projects for the Kustomize generator.
//
// opencharly/spec#304 unit 5 moves the authored `#Kubernetes` / `#Kindcluster` bodies (and the
// Go types generated from them) into the plugin that SERVES the kinds, so this plugin can no
// longer decode the kindcluster body into `spec.Kindcluster` nor build a `spec.Kubernetes` for
// the projection. The wire was already opaque (Cutover K): the template body arrives as raw
// JSON and `#KubernetesGenerateKustomizeRequest.ClusterJSON` is a bytes field — so only the
// SHAPES this plugin reads and writes need a home, and this is it.
//
// The kinds' own CUE bodies declare the full surface; these structs carry exactly the fields
// this plugin reads, with the authored JSON tags so the projection's JSON stays byte-identical.

// kindclusterSpec is the kindcluster body this plugin provisions from.
type kindclusterSpec struct {
	Box               string                 `json:"box"`
	KubeconfigContext string                 `json:"kubeconfig_context"`
	DefaultNamespace  string                 `json:"default_namespace"`
	AdmissionPolicy   string                 `json:"admission_policy"`
	Defaults          kubernetesResourceView `json:"defaults"`
	Storage           kubernetesStorageView  `json:"storage"`
	Ingress           kubernetesIngressView  `json:"ingress"`
	ImageDefault      kubernetesImageView    `json:"image_default"`
	PodDefault        kubernetesPodView      `json:"pod_default"`

	// engine selects the container engine kind provisions its node containers with.
	Engine string `json:"engine"`
	// node_image is the digest-pinned kind node image (kindest/node:vX@sha256:…).
	NodeImage string `json:"node_image"`
	// nodes is the cluster topology. Absent -> a single control-plane node.
	Nodes []kindclusterNodeView `json:"nodes"`
}

// kindclusterNodeView is one node in the cluster topology.
type kindclusterNodeView struct {
	Role              string                `json:"role"`
	Image             string                `json:"image"`
	ExtraPortMappings []kindPortMappingView `json:"extra_port_mappings"`
}

// kindPortMappingView maps a node port to a host port (kind extraPortMappings).
type kindPortMappingView struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	ListenAddress string `json:"listen_address"`
	Protocol      string `json:"protocol"`
}

// kubernetesPolicy is the `kubernetes:` cluster-policy body the Kustomize generator consumes —
// the projection target. Its JSON tags are the authored ones, so a kindcluster template
// projects onto EXACTLY the bytes a kubernetes template would send.
type kubernetesPolicy struct {
	Box               string                `json:"box"`
	KubeconfigContext string                `json:"kubeconfig_context,omitempty"`
	Kubeconfig        string                `json:"kubeconfig,omitempty"`
	AdmissionPolicy   string                `json:"admission_policy,omitempty"`
	DefaultNamespace  string                `json:"default_namespace,omitempty"`
	Storage           kubernetesStorageView `json:"storage,omitempty"`
	Ingress           kubernetesIngressView `json:"ingress,omitempty"`
	ImageDefault      kubernetesImageView   `json:"image_default,omitempty"`
	PodDefault        kubernetesPodView     `json:"pod_default,omitempty"`
	// kubevirt carries the cluster-side KubeVirt knobs. The kindcluster projection never sets
	// it (a kind cluster is not a KubeVirt venue), but the wire the generator consumes carries
	// the key, so the field is here to keep the projection's bytes identical.
	KubeVirt kubernetesKubeVirtView `json:"kubevirt,omitempty"`
	Defaults kubernetesResourceView `json:"defaults,omitempty"`
}

// kubernetesStorageView mirrors the authored `storage:` sub-block.
type kubernetesStorageView struct {
	ClassDefault      string `json:"class_default,omitempty"`
	ClassCheap        string `json:"class_cheap,omitempty"`
	ClassEncrypted    string `json:"class_encrypted,omitempty"`
	ClassFast         string `json:"class_fast,omitempty"`
	AccessModeDefault string `json:"access_mode_default,omitempty"`
}

// kubernetesIngressView mirrors the authored `ingress:` sub-block.
type kubernetesIngressView struct {
	Enabled         bool   `json:"enabled,omitempty"`
	Class           string `json:"class,omitempty"`
	CertIssuer      string `json:"cert_issuer,omitempty"`
	PathTypeDefault string `json:"path_type_default,omitempty"`
}

// kubernetesImageView mirrors the authored `image_default:` sub-block.
type kubernetesImageView struct {
	PullPolicy  string   `json:"pull_policy,omitempty"`
	PullSecrets []string `json:"pull_secrets,omitempty"`
}

// kubernetesPodView mirrors the authored `pod_default:` sub-block.
type kubernetesPodView struct {
	PriorityClass string            `json:"priority_class,omitempty"`
	Tolerations   []map[string]any  `json:"tolerations,omitempty"`
	NodeSelector  map[string]string `json:"node_selector,omitempty"`
}

// kubernetesKubeVirtView mirrors the authored `kubevirt:` sub-block (never populated by the
// kindcluster projection; present so the wire keeps its key).
type kubernetesKubeVirtView struct {
	Enabled             bool     `json:"enabled,omitempty"`
	CDIEnabled          bool     `json:"cdi_enabled,omitempty"`
	CDINamespace        string   `json:"cdi_namespace,omitempty"`
	UseEmulation        bool     `json:"use_emulation,omitempty"`
	DefaultStorageClass string   `json:"default_storage_class,omitempty"`
	DefaultInstancetype string   `json:"default_instancetype,omitempty"`
	DefaultPreference   string   `json:"default_preference,omitempty"`
	SnapshotClass       string   `json:"snapshot_class,omitempty"`
	VirtctlVersion      string   `json:"virtctl_version,omitempty"`
	FeatureGates        []string `json:"feature_gates,omitempty"`
}

// kubernetesResourceView mirrors the authored `defaults:` sub-block.
type kubernetesResourceView struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// kindclusterAsKubernetes projects a kindcluster cluster template onto the kubernetes policy
// body the Kustomize generator consumes — one policy surface for both cluster kinds (R3).
// Only the shared policy + identity fields carry; kindcluster's kind-specific topology
// (nodes/node_image/engine) is not part of the workload generation.
func kindclusterAsKubernetes(kc *kindclusterSpec) kubernetesPolicy {
	return kubernetesPolicy{
		Box:               kc.Box,
		KubeconfigContext: kc.KubeconfigContext,
		DefaultNamespace:  kc.DefaultNamespace,
		AdmissionPolicy:   kc.AdmissionPolicy,
		Defaults:          kc.Defaults,
		Storage:           kc.Storage,
		Ingress:           kc.Ingress,
		ImageDefault:      kc.ImageDefault,
		PodDefault:        kc.PodDefault,
	}
}
