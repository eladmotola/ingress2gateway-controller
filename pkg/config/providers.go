package config

const (
	// Provider identifiers
	ProviderIngressNginx = "ingress-nginx"
	ProviderNginx        = "nginx"
	ProviderIstio        = "istio"
	ProviderKong         = "kong"
	ProviderGCE          = "gce"
	ProviderApisix       = "apisix"
	ProviderCilium       = "cilium"
)

// SupportedProviders maps supported provider names to a boolean
var SupportedProviders = map[string]bool{
	ProviderIngressNginx: true,
	ProviderIstio:        true,
	ProviderKong:         true,
	ProviderGCE:          true,
	ProviderApisix:       true,
	ProviderCilium:       true,
	ProviderNginx:        true,
}
