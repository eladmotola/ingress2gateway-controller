//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"io"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ---------------------------------------------------------------------------
// Global state (set once in TestMain, read-only in tests)
// ---------------------------------------------------------------------------

var (
	k8sClient      client.Client
	kubeconfigPath string
	clusterName    string
	artifactsDir   string
)

const (
	helmReleaseName = "i2g-e2e"
	helmNamespace   = "i2g-system"

	// Image loaded into kind. Must match the override values in testdata/values-*.yaml.
	imageRegistry   = "i2g-e2e-local"
	imageRepository = "ingress2gateway-controller"
	imageTag        = "e2e"
	controllerImage = imageRegistry + "/" + imageRepository + ":" + imageTag

	// Timing
	defaultTimeout  = 60 * time.Second
	defaultInterval = 500 * time.Millisecond
	negativeWait    = 6 * time.Second // how long to poll when asserting "nothing created"
)

func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.WriteTo(io.Discard)))

	clusterName = getEnvOrDefault("E2E_CLUSTER_NAME", "i2g-e2e")
	artifactsDir = getEnvOrDefault("E2E_ARTIFACTS_DIR", "./e2e-artifacts")
	useExisting := os.Getenv("E2E_USE_EXISTING_CLUSTER") == "true"
	keepCluster := os.Getenv("E2E_KEEP_CLUSTER") == "true"

	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create artifacts dir: %v\n", err)
	}

	// ── 1. Cluster ───────────────────────────────────────────────────────────
	if !useExisting {
		// Drop a leftover cluster from a previous failed run so create cannot collide.
		_ = runCmdSilent("kind", "delete", "cluster", "--name", clusterName)
		fmt.Printf("[e2e] Creating kind cluster %q\n", clusterName)
		if err := runCmdSilent("kind", "create", "cluster", "--name", clusterName, "--wait", "120s"); err != nil {
			fmt.Fprintf(os.Stderr, "[e2e] failed to create kind cluster: %v\n", err)
			os.Exit(1)
		}
	}

	// ── 2. Kubeconfig ────────────────────────────────────────────────────────
	kubeconfigPath = filepath.Join(os.TempDir(), "e2e-kubeconfig-"+clusterName)
	if err := runCmdSilent("kind", "export", "kubeconfig", "--name", clusterName, "--kubeconfig", kubeconfigPath); err != nil {
		fmt.Fprintf(os.Stderr, "[e2e] failed to export kubeconfig: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("KUBECONFIG", kubeconfigPath); err != nil {
		fmt.Fprintf(os.Stderr, "[e2e] failed to set KUBECONFIG: %v\n", err)
		os.Exit(1)
	}

	// ── 3. Docker image (always rebuild/load so an existing cluster still gets a fresh binary)
	repoRoot := findRepoRoot()
	fmt.Printf("[e2e] Building image %s from %s\n", controllerImage, repoRoot)
	if err := runCmdSilent("docker", "build", "-t", controllerImage, repoRoot); err != nil {
		fmt.Fprintf(os.Stderr, "[e2e] docker build failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[e2e] Loading image into kind cluster %q\n", clusterName)
	if err := runCmdSilent("kind", "load", "docker-image", controllerImage, "--name", clusterName); err != nil {
		fmt.Fprintf(os.Stderr, "[e2e] kind load failed: %v\n", err)
		os.Exit(1)
	}

	// ── 4. Gateway API CRDs (experimental channel, v1.5.0) ──────────────────
	crdURL := "https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.5.0/experimental-install.yaml"
	fmt.Println("[e2e] Installing Gateway API CRDs (experimental v1.5.0, server-side apply)...")
	// HTTPRoute CRD annotations exceed the 256Ki last-applied-configuration limit
	// of client-side apply. Server-side apply is what Gateway API documents.
	if err := runCmdSilent("kubectl", "--kubeconfig", kubeconfigPath,
		"apply", "--server-side", "--force-conflicts", "-f", crdURL); err != nil {
		fmt.Fprintf(os.Stderr, "[e2e] failed to install Gateway API CRDs: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[e2e] Waiting for Gateway API CRDs to be Established...")
	if err := waitForCRDsEstablished(120 * time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "[e2e] CRDs not established: %v\n", err)
		os.Exit(1)
	}

	// ── 5. k8s client ────────────────────────────────────────────────────────
	scheme := clientgoscheme.Scheme
	if err := gatewayv1.Install(scheme); err != nil {
		fmt.Fprintf(os.Stderr, "[e2e] failed to add Gateway API to scheme: %v\n", err)
		os.Exit(1)
	}
	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[e2e] failed to build rest config: %v\n", err)
		os.Exit(1)
	}
	k8sClient, err = client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[e2e] failed to build k8s client: %v\n", err)
		os.Exit(1)
	}

	// ── 6. Run tests ─────────────────────────────────────────────────────────
	code := m.Run()

	// ── 7. Teardown ──────────────────────────────────────────────────────────
	if !useExisting {
		// Best-effort helm uninstall so the cluster teardown is clean
		_ = runCmdSilent("helm", "uninstall", helmReleaseName, "--kubeconfig", kubeconfigPath, "--namespace", helmNamespace, "--ignore-not-found")
	}
	if !useExisting && !keepCluster {
		fmt.Printf("[e2e] Deleting kind cluster %q\n", clusterName)
		_ = runCmdSilent("kind", "delete", "cluster", "--name", clusterName)
	}

	os.Exit(code)
}

// ---------------------------------------------------------------------------
// TestE2ESuite — the single entry point that sequences all groups.
// Groups run sequentially; between groups we helm-upgrade to the next config
// and wait for rollout.  Each group's t.Cleanup handlers delete their
// namespaces (with --wait) before this function proceeds to the next upgrade,
// so the controller never reconciles stale Ingresses under the wrong flags.
// ---------------------------------------------------------------------------

func TestE2ESuite(t *testing.T) {
	// ── Group A: annotation mode ─────────────────────────────────────────────
	helmInstallOrUpgrade(t, testdataPath("values-a.yaml"), true /*wait*/)
	waitForRollout(t)

	t.Run("GroupA", groupA)
	// All GroupA namespaces cleaned up by this point (sequential sub-test cleanups)

	// ── Group B: ingressclass mode ────────────────────────────────────────────
	helmInstallOrUpgrade(t, testdataPath("values-b.yaml"), true)
	waitForRollout(t)

	t.Run("GroupB", groupB)

	// ── Group C: ingressclass + external gateway ──────────────────────────────
	helmInstallOrUpgrade(t, testdataPath("values-c.yaml"), true)
	waitForRollout(t)

	t.Run("GroupC", groupC)

	// ── Group D: startup validation (uses a separate helm release) ────────────
	t.Run("GroupD", groupD)

	// ── Group E: optional — restart / finalizer ────────────────────────────────
	// Restore to annotation-mode config used by Group A
	helmInstallOrUpgrade(t, testdataPath("values-a.yaml"), true)
	waitForRollout(t)

	t.Run("GroupE", groupE)

	// Final cleanup: uninstall the main release
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_ = ctx
		_ = runCmdSilent("helm", "uninstall", helmReleaseName,
			"--kubeconfig", kubeconfigPath,
			"--namespace", helmNamespace, "--ignore-not-found")
	})
}

// getEnvOrDefault returns the value of env var key or the given default.
func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
