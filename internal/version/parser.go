package version

import "context"

type Parser interface {
	GetVersion(ctx context.Context) (string, error)
}
