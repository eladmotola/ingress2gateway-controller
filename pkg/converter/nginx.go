package converter

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	nginxProxySSLSecretAnnotation = "nginx.ingress.kubernetes.io/proxy-ssl-secret"
)

// fetchNginxSpecificObjects retrieves cluster objects that are specifically needed
// by the ingress-nginx provider (such as the CA/client secret referenced by proxy-ssl-secret).
func fetchNginxSpecificObjects(ctx context.Context, c client.Client, ingresses []networkingv1.Ingress) []client.Object {
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
		if err := c.Get(ctx, nn, sec); err == nil {
			objects = append(objects, sec)
		}
	}

	return objects
}
