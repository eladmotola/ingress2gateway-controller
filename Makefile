.PHONY: help build run test coverage docker-build deploy clean e2e e2e-cluster-up e2e-cluster-down

# Variables
IMAGE_NAME ?= ingress2gateway-controller
IMAGE_TAG ?= latest
REGISTRY ?= localhost:5001
E2E_CLUSTER_NAME ?= i2g-e2e
E2E_IMAGE ?= i2g-e2e-local/ingress2gateway-controller:e2e

help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

build: ## Build the controller binary
	go build -o controller ./cmd/controller

run: ## Run the controller locally
	go run cmd/controller/main.go

test: ## Run tests (excludes e2e; those require -tags e2e)
	go test -v ./...

coverage: ## Run tests with coverage and print a summary
	CGO_ENABLED=1 && go test -race -covermode=atomic -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

fmt: ## Format code
	go fmt ./...

vet: ## Run go vet
	go vet ./...

lint: fmt vet ## Run all linters

##@ Dependencies

deps: ## Download dependencies
	go mod download

tidy: ## Tidy go.mod
	go mod tidy

##@ Docker

docker-build: ## Build Docker image
	docker build -t $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) .

docker-push: docker-build ## Build and push Docker image
	docker push $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

##@ Deployment

deploy: ## Deploy to Kubernetes
	kubectl apply -f deploy/kubernetes/namespace.yaml
	kubectl apply -f deploy/kubernetes/rbac.yaml
	kubectl apply -f deploy/kubernetes/deployment.yaml
	kubectl apply -f deploy/kubernetes/service.yaml

undeploy: ## Remove from Kubernetes
	kubectl delete -f deploy/kubernetes/ --ignore-not-found=true

logs: ## Show controller logs
	kubectl logs -n ingress2gateway-system -l app=ingress2gateway-controller -f

##@ Helm

helm-lint: ## Lint the Helm chart
	helm lint helm/ingress2gateway-controller

helm-template: ## Template the Helm chart
	helm template ingress2gateway-controller helm/ingress2gateway-controller \
		--namespace ingress2gateway-system

helm-install: ## Install the Helm chart
	helm install ingress2gateway-controller helm/ingress2gateway-controller \
		--namespace ingress2gateway-system \
		--create-namespace

helm-upgrade: ## Upgrade the Helm chart
	helm upgrade ingress2gateway-controller helm/ingress2gateway-controller \
		--namespace ingress2gateway-system

helm-uninstall: ## Uninstall the Helm chart
	helm uninstall ingress2gateway-controller -n ingress2gateway-system

##@ E2E

e2e: ## Build the controller image and run kind/helm end-to-end tests
	docker build -t $(E2E_IMAGE) .
	go test -tags e2e -timeout 20m -v ./test/e2e/...

e2e-cluster-up: ## Create a kind cluster for iterating on e2e tests
	kind create cluster --name $(E2E_CLUSTER_NAME) --wait 120s

e2e-cluster-down: ## Delete the e2e kind cluster
	kind delete cluster --name $(E2E_CLUSTER_NAME)

##@ Cleanup

clean: ## Clean build artifacts
	rm -f controller
	go clean

##@ Examples

example-httproute: ## Apply HTTPRoute-only mode example
	kubectl apply -f examples/httproute-only-mode.yaml

example-full: ## Apply full conversion example
	kubectl apply -f examples/full-conversion-with-tls.yaml

example-multi: ## Apply multi-provider example
	kubectl apply -f examples/multi-provider.yaml
