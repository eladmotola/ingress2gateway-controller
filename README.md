# Ingress2Gateway Controller

[![Go Report Card](https://goreportcard.com/badge/github.com/eladm/ingress2gateway-controller)](https://goreportcard.com/report/github.com/eladm/ingress2gateway-controller)

The `ingress2gateway-controller` is a Kubernetes operator that automatically and continuously converts standard **Ingress** resources into the modern **Gateway API** resources (`Gateway` and `HTTPRoute`).

It bridges the gap between the legacy Ingress API and the powerful, role-oriented Gateway API, allowing you to migrate your traffic management at your own pace while maintaining a single source of truth.

## Why Ingress2Gateway?

*   **Automated Migration**: Stop manually writing Gateway API manifests. The controller does it for you.
*   **Continuous Synchronization**: Any changes to your Ingress resources are immediately reflected in the corresponding Gateway API resources.
*   **Provider Specific Logic**: Leverages the `ingress2gateway` library to support complex provider-specific annotations (e.g., Nginx, Istio).
*   **Infrastructure Agnostic**: Supports multiple Ingress controllers and Gateway API implementations.

## How it Works

The controller watches for `Ingress` resources that have the `ingress2gateway.io/gateway-class` annotation. When a change is detected, it performs the conversion and manages the lifecycle of the resulting `Gateway` and `HTTPRoute` resources.

## Supported Providers

The controller currently supports conversion from the following Ingress providers:

*   **nginx**
*   **ingress-nginx**
*   **istio**
*   **kong**
*   **gce**
*   **apisix**
*   **cilium**

## Supported Emitters

*   **standard** (default)
*   **envoy-gateway**
*   **gce**
*   **kgateway**

### note

The following list was copied from the `ingress2gateway` library README.md


## Getting Started

### Prerequisites

*   Kubernetes 1.27+
*   Helm 3.0+
*   [Gateway API CRDs](https://gateway-api.sigs.k8s.io/guides/#install-gateway-api-crds) installed (>=1.5.0)

### Quick Installation

1.  **Install Gateway API CRDs**:
    ```bash
    kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.5.0/standard-install.yaml
    ```

2.  **Install the Controller via Helm**:
    ```bash
    helm install ingress2gateway-controller oci://ghcr.io/eladmotola/ingress2gateway-controller \
      --namespace ingress2gateway-system \
      --create-namespace
    ```

## Configuration

The controller is driven by annotations on your Ingress resources.

### Mandatory Configuration

This configuration is **mandatory** in order to make the controller work. You must provide a mapping between your Ingress classes and the target Gateway classes in your Helm values (`values.yaml`):

```yaml
controller:
  ingressClassToGatewayClassMapping:
    nginx-internal: nginx
    nginx-external: nginx
```

### Required Annotations

| Annotation | Description |
|------------|-------------|
| `ingress2gateway.io/enabled` | Enable conversion for the Ingress. |

### Optional Annotations

| Annotation | Description | Default |
|------------|-------------|---------|
| `ingress2gateway.io/provider` | The source provider logic to use (e.g., `nginx`, `istio`). | Global default |
| `ingress2gateway.io/gateway-namespace` | The namespace where the `Gateway` resource will be created. | Ingress namespace |
| `ingress2gateway.io/route-namespace` | The namespace where the `HTTPRoute` or `GRPCRoute` resource will be created. | Ingress namespace |

## Example

Annotate an existing Ingress to start the conversion:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-app
  annotations:
    ingress2gateway.io/enabled: "true"
spec:
  ingressClassName: nginx-internal
  rules:
  - host: my-app.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: my-app-service
            port:
              number: 80
```

The controller will automatically create a `Gateway` (if it doesn't exist) and an `HTTPRoute` (or `GRPCRoute`) pointing to `my-app-service`.
