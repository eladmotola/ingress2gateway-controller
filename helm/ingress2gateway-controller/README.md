# Helm Chart Commands

This file contains Helm-specific commands for the Ingress2Gateway Controller.

## Prerequisites

- Helm 3.0+
- kubectl configured with cluster access
- Gateway API CRDs installed (>=1.5.0)

## Validate Chart

### Lint the Chart

```bash
helm lint helm/ingress2gateway-controller
```

### Template the Chart (Dry-run)

```bash
helm template ingress2gateway-controller helm/ingress2gateway-controller \
  --namespace ingress2gateway-system
```

### Template with Custom Values

```bash
helm template ingress2gateway-controller helm/ingress2gateway-controller \
  --namespace ingress2gateway-system \
  --set replicaCount=3 \
  --set image.tag=latest
```

## Install Chart

### Basic Installation (From OCI Registry)

```bash
# Replace <version> with the desired version (e.g., 0.1.0)
helm install ingress2gateway-controller oci://ghcr.io/eladmotola/ingress2gateway-controller \
  --version <version> \
  --namespace ingress2gateway-system \
  --create-namespace
```

### Install from local source

```bash
helm install ingress2gateway-controller ./helm/ingress2gateway-controller \
  --namespace ingress2gateway-system \
  --create-namespace
```

### Install with CLI Overrides

```bash
helm install ingress2gateway-controller oci://ghcr.io/eladmotola/ingress2gateway-controller \
  --version <version> \
  --namespace ingress2gateway-system \
  --create-namespace \
  --set replicaCount=3 \
  --set image.tag=<version> \
  --set resources.requests.cpu=200m \
  --set resources.requests.memory=256Mi
```

## Upgrade Chart

### Upgrade with New Values

```bash
helm upgrade ingress2gateway-controller oci://ghcr.io/eladmotola/ingress2gateway-controller \
  --version <version> \
  --namespace ingress2gateway-system \
  -f custom-values.yaml
```

### Upgrade with Inline Values

```bash
helm upgrade ingress2gateway-controller oci://ghcr.io/eladmotola/ingress2gateway-controller \
  --version <version> \
  --namespace ingress2gateway-system \
  --set image.tag=v1.1.0
```

### Upgrade with Reuse Values

```bash
helm upgrade ingress2gateway-controller oci://ghcr.io/eladmotola/ingress2gateway-controller \
  --version <version> \
  --namespace ingress2gateway-system \
  --reuse-values \
  --set image.tag=v1.1.0
```

## Configuration Options

| Parameter | Description | Default |
|-----------|-------------|---------|
| `controller.defaultProvider` | Default Ingress provider | `ingress-nginx` |
| `controller.defaultEmitter` | Default emitter | `standard` |
| `controller.ingressClassToExternalGatewayMapping` | Map of IngressClass to external Gateway (`namespace/name`) | `{}` |
| `controller.ingressClassToGatewayClassMapping` | Map of IngressClass to GatewayClass | `{}` |

### Selection Modes

*   **`annotation` (default)**: Only Ingresses with `ingress2gateway.io/enabled: "true"` are converted.
*   **`ingressclass`**: Automatically selects any Ingress whose class matches an entry in `ingressClassToGatewayClassMapping` or `ingressClassToExternalGatewayMapping`. Ingresses without a matching class are ignored (even if `ingress2gateway.io/enabled: "true"` is set).
*   In both modes, `ingress2gateway.io/enabled: "false"` can be used to explicitly opt out.

> [!WARNING]
> In `ingressclass` mode, every selected Ingress gets the controller's finalizer (`ingress2gateway.io/finalizer`). Deleting those Ingresses blocks while the controller is down.

### External Gateways

Use `controller.ingressClassToExternalGatewayMapping` to configure pre-existing Gateways that the controller should attach routes to without managing the Gateway itself:

```yaml
controller:
  ingressSelectionMode: "ingressclass"
  ingressClassToExternalGatewayMapping:
    nginx-external: gateway-infra/my-gateway
```

> [!IMPORTANT]
> The external Gateway's listeners must cover the required hostnames and allow routes from the Ingress namespaces via `allowedRoutes.namespaces`, otherwise routes will fail to attach.

### Global Mapping for Managed Gateways

When using managed Gateways, provide the mapping in your `values.yaml`:

```yaml
controller:
  ingressClassToGatewayClassMapping:
    nginx-internal: nginx
    nginx-external: nginx
```

## Uninstall Chart

```bash
helm uninstall ingress2gateway-controller -n ingress2gateway-system
```