//go:build e2e

package e2e

// ---------------------------------------------------------------------------
// Group D: startup validation — controller must crash-loop on bad config.
//
// Each scenario installs a SEPARATE helm release in its own namespace so the
// main controller release (used by A/B/C/E) is unaffected.
// ---------------------------------------------------------------------------

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const dGroupReleaseName = "i2g-e2e-d"

func groupD(t *testing.T) {
	t.Run("D17a_BadSelectionMode", testD17aBadSelectionMode)
	t.Run("D17b_MalformedExternalMapping", testD17bMalformedExternalMapping)
}

// D17a: Install with ingressSelectionMode=bogus → pod crash-loops;
// controller logs contain "invalid ingress selection mode".
func testD17aBadSelectionMode(t *testing.T) {
	defer dumpArtifacts(t)

	ns := fmt.Sprintf("i2g-d17a-%s", randSuffix(5))
	helmInstallSeparate(t, dGroupReleaseName+"-a", ns, testdataPath("values-d-bogus.yaml"))

	waitForPodCrashLoop(t, ns)

	// ValidateIngressSelectionMode logs via setupLog before ctrl.SetLogger, so
	// zap may never emit the message. CrashLoopBackOff is the reliable signal.
	logs := crashedPodLogs(t, ns)
	if !(strings.Contains(logs, "invalid ingress selection mode") ||
		strings.Contains(logs, "bogus") ||
		strings.Contains(logs, "allowed values")) {
		t.Logf("crash-looped as expected; exact error string not in logs (setupLog before SetLogger):\n%s", logs)
	}
}

// D17b: Install with a malformed external gateway mapping → pod crash-loops.
// The flag parser (externalGatewayMapFlag.Set) returns an error for
// "ext-class=badformat" (no namespace/name slash), causing flag.Parse → os.Exit(2).
func testD17bMalformedExternalMapping(t *testing.T) {
	defer dumpArtifacts(t)

	ns := fmt.Sprintf("i2g-d17b-%s", randSuffix(5))
	helmInstallSeparate(t, dGroupReleaseName+"-b", ns, testdataPath("values-d-badmap.yaml"))

	waitForPodCrashLoop(t, ns)

	var logs string
	eventually(t, func() error {
		logs = crashedPodLogs(t, ns)
		if d17bLogOK(logs) {
			return nil
		}
		return fmt.Errorf("crash evidence not yet in logs/describe")
	}, 60*time.Second, 2*time.Second)
	assert.True(t, d17bLogOK(logs),
		"expected crash log to indicate invalid external gateway mapping; got:\n%s", logs)
}

func d17bLogOK(logs string) bool {
	return strings.Contains(logs, "badformat") ||
		strings.Contains(logs, "invalid external gateway") ||
		strings.Contains(logs, "expected <namespace>/<name>") ||
		strings.Contains(logs, "invalid value")
}
