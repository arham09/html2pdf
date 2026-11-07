package html2pdf

import (
	"errors"
	"fmt"

	"github.com/arham09/html2pdf/internal/session"
)

// Error categories support errors.Is; errors retain context and I/O causes.
var (
	ErrInvalidOptions     = session.ErrInvalidOptions
	ErrInvalidInput       = session.ErrInvalidInput
	ErrLimitExceeded      = session.ErrLimitExceeded
	ErrUnsupportedFeature = session.ErrUnsupportedFeature
	ErrMissingResource    = session.ErrMissingResource
	ErrUnbreakableContent = session.ErrUnbreakableContent
)

// LimitError reports the budget that failed, without document content. Get it
// with errors.As; errors.Is(err, ErrLimitExceeded) is also true.
type LimitError struct {
	Resource  string // budget name, such as "pages", "glyphs" or "output_bytes"
	Limit     int64  // the configured ceiling
	Used      int64  // the amount used before the failed step
	Requested int64  // the amount that the failed step asked for
	err       error  // the internal error, with its context
}

func (e *LimitError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("%s: %s (limit %d, used %d, requested %d)", ErrLimitExceeded, e.Resource, e.Limit, e.Used, e.Requested)
}

func (e *LimitError) Unwrap() error {
	if e.err != nil {
		return e.err
	}
	return ErrLimitExceeded
}

// publicError exposes an internal quota failure as the exported LimitError.
// The message and the errors.Is chain do not change.
func publicError(err error) error {
	var limit *session.LimitError
	if !errors.As(err, &limit) {
		return err
	}
	return &LimitError{Resource: limit.Resource, Limit: limit.Limit, Used: limit.Used, Requested: limit.Requested, err: err}
}
