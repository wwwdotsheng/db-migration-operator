package resolver

import (
	"context"
	"fmt"

	"github.com/wwwdotsheng/db-migration-operator/internal/adapter"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type SecretResolver struct {
	reader client.Reader
}

func NewSecretResolver(reader client.Reader) *SecretResolver {
	return &SecretResolver{reader: reader}
}
func (s *SecretResolver) ResolvePassword(
	ctx context.Context,
	namespace string,
	ref corev1.SecretKeySelector,
) (string, error) {
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: namespace, Name: ref.Name}
	if err := s.reader.Get(ctx, key, &secret); err != nil {
		switch {
		case apierrors.IsNotFound(err):
			return "", fmt.Errorf("secret %s: %w", key, adapter.ErrNotFoundSecret)
		case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
			return "", fmt.Errorf("secret %s: %w", key, adapter.ErrUnauthorizedSecret)
		default:
			return "", fmt.Errorf("get secret %s: %w", key, err)
		}
	}
	v, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("secret %s: key %q: %w", key, ref.Key, adapter.ErrNotFoundSecret)
	}
	return string(v), nil
}
