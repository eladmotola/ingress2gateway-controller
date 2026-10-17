package converter

import (
	"context"
	"testing"

	"github.com/kubernetes-sigs/ingress2gateway/pkg/i2gw/notifications"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFetchNginxIngressSpecificObjects(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))

	secret1 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ssl-secret-same-ns",
			Namespace: "default",
		},
	}
	secret2 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ssl-secret-other-ns",
			Namespace: "other",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret1, secret2).Build()
	ctx := context.Background()

	t.Run("no annotations", func(t *testing.T) {
		ing := networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ing1",
				Namespace: "default",
			},
		}
		objs := fetchNginxIngressSpecificObjects(ctx, fakeClient, []networkingv1.Ingress{ing}, nil)
		assert.Empty(t, objs)
	})

	t.Run("same namespace and cross namespace secrets with deduplication", func(t *testing.T) {
		ing1 := networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ing1",
				Namespace: "default",
				Annotations: map[string]string{
					nginxProxySSLSecretAnnotation: "ssl-secret-same-ns",
				},
			},
		}
		ing2 := networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ing2",
				Namespace: "default",
				Annotations: map[string]string{
					nginxProxySSLSecretAnnotation: "other/ssl-secret-other-ns",
				},
			},
		}
		// Duplicate reference
		ing3 := networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ing3",
				Namespace: "default",
				Annotations: map[string]string{
					nginxProxySSLSecretAnnotation: "default/ssl-secret-same-ns",
				},
			},
		}

		objs := fetchNginxIngressSpecificObjects(ctx, fakeClient, []networkingv1.Ingress{ing1, ing2, ing3}, nil)
		assert.Len(t, objs, 2)
	})

	t.Run("secret not found adds notification report", func(t *testing.T) {
		ing := networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "missing-sec-ing",
				Namespace: "default",
				Annotations: map[string]string{
					nginxProxySSLSecretAnnotation: "non-existent-secret",
				},
			},
		}
		report := notifications.NewReport()
		objs := fetchNginxIngressSpecificObjects(ctx, fakeClient, []networkingv1.Ingress{ing}, &report)
		assert.Empty(t, objs)
	})
}
