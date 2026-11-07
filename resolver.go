package html2pdf

import (
	"context"
	"io"
)

// AssetResolver is the only route for external assets. Implementations must
// enforce their own scheme/host/path allowlists, obey ctx and allow concurrent
// calls. The engine owns and closes returned readers, including on failure.
type AssetResolver interface {
	Resolve(ctx context.Context, reference string) (io.ReadCloser, error)
}
