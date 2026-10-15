package converter

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/notifications"
)

const (
	nginxProxySSLSecretAnnotation = "nginx.ingress.kubernetes.io/proxy-ssl-secret"
)

// fetchNginxIngressSpecificObjects retrieves cluster objects that are specifically needed
// by the ingress-nginx provider (such as the CA/client secret referenced by proxy-ssl-secret).
func fetchNginxIngressSpecificObjects(ctx context.Context, c client.Client, ingresses []networkingv1.Ingress, report *notifications.Report) []client.Object {
	logger := log.FromContext(ctx)
	var objects []client.Object
	visitedSecret := make(map[types.NamespacedName]bool)

	for _, ing := range ingresses {
		sslSecret, ok := ing.Annotations[nginxProxySSLSecretAnnotation]
		if !ok || strings.TrimSpace(sslSecret) == "" {
			continue
		}

		parts := strings.Split(sslSecret, "/")
		var nn types.NamespacedName
		if len(parts) == 2 {
			nn = types.NamespacedName{
				Namespace: strings.TrimSpace(parts[0]),
				Name:      strings.TrimSpace(parts[1]),
			}
		} else {
			nn = types.NamespacedName{
				Namespace: ing.Namespace,
				Name:      strings.TrimSpace(parts[0]),
			}
		}

		if visitedSecret[nn] {
			continue
		}
		visitedSecret[nn] = true

		sec := &corev1.Secret{}
		if err := c.Get(ctx, nn, sec); err != nil {
			logger.Error(err, "Failed to fetch proxy-ssl-secret", "secret", nn.String(), "ingress", ing.Namespace+"/"+ing.Name)
			if report != nil {
				report.Add(fmt.Sprintf("%s/%s", ing.Namespace, ing.Name), notifications.Notification{
					Type:    notifications.WarningNotification,
					Message: fmt.Sprintf("proxy-ssl-secret %s could not be fetched: %v", nn.String(), err),
				})
			}
		} else {
			objects = append(objects, sec)
		}
	}

	return objects
}
