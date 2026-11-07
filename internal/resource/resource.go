// Package resource performs explicit, bounded asset loading. It provides no
// network client, filesystem resolver, CSS import, or host font discovery.
package resource

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/url"
	"strings"

	"github.com/arham09/html2pdf/internal/session"
)

type Resolver interface {
	Resolve(context.Context, string) (io.ReadCloser, error)
}
type Kind uint8

const (
	Image Kind = iota
	Stylesheet
	Font
)

type Loader struct {
	Session  *session.Session
	Resolver Resolver
}

// Load owns/closes the returned reader and accounts for unsuccessful attempts.
// It does not cache across renders or silently recover missing resources.
func (l Loader) Load(kind Kind, reference string) (data []byte, err error) {
	s := l.Session
	if s == nil || reference == "" || kind > Font {
		return nil, fmt.Errorf("%w: invalid resource request", session.ErrInvalidInput)
	}
	if err = s.CheckSize("resource_uri_bytes", int64(len(reference))); err != nil {
		return nil, err
	}
	if err = s.Charge("assets", 1); err != nil {
		return nil, err
	}
	var reader io.ReadCloser
	if strings.HasPrefix(strings.ToLower(reference), "data:") {
		// Account for the encoded representation as well as decoded payloads.
		if err = s.Charge("total_asset_bytes", int64(len(reference))); err != nil {
			return nil, err
		}
		comma := strings.IndexByte(reference, ',')
		if comma < 0 {
			return nil, fmt.Errorf("%w: malformed data URL", session.ErrInvalidInput)
		}
		header, payload := reference[:comma], reference[comma+1:]
		if strings.HasSuffix(strings.ToLower(header), ";base64") {
			reader = io.NopCloser(base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(payload)))
		} else {
			decoded, e := url.PathUnescape(payload)
			if e != nil {
				return nil, fmt.Errorf("%w: malformed data URL", session.ErrInvalidInput)
			}
			reader = io.NopCloser(strings.NewReader(decoded))
		}
	} else {
		if l.Resolver == nil {
			return nil, fmt.Errorf("%w: external access requires a resolver", session.ErrMissingResource)
		}
		reader, err = l.Resolver.Resolve(s.Context, reference)
	}
	if reader != nil {
		defer func() {
			err = errors.Join(err, reader.Close())
			if err != nil {
				data = nil
			}
		}()
	}
	if err != nil {
		if errors.Is(err, session.ErrMissingResource) {
			return nil, fmt.Errorf("resolver: %w", err)
		}
		return nil, fmt.Errorf("%w: resolver: %w", session.ErrMissingResource, err)
	}
	if err = s.Check(); err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, fmt.Errorf("%w: resolver returned no reader", session.ErrMissingResource)
	}
	ceiling := s.Limits.MaxAssetBytes
	switch kind {
	case Font:
		ceiling = min(ceiling, s.Limits.MaxFontBytes)
	case Stylesheet:
		ceiling = min(ceiling, s.Limits.MaxCSSBytes)
	}
	limited := io.LimitReader(reader, ceiling+1)
	buf := make([]byte, 4096)
	var total int64
	idle := 0
	for {
		if err = s.Check(); err != nil {
			return nil, err
		}
		n, e := limited.Read(buf)
		if n > 0 {
			idle = 0
			total += int64(n)
			if total > ceiling {
				return nil, &session.LimitError{Resource: "asset_bytes", Limit: ceiling, Requested: total}
			}
			if err = s.Charge("total_asset_bytes", int64(n)); err != nil {
				return nil, err
			}
			data = append(data, buf[:n]...)
		} else {
			idle++
			if e == nil && idle >= 100 {
				return nil, io.ErrNoProgress
			}
		}
		if e != nil {
			if errors.Is(e, io.EOF) {
				break
			}
			return nil, fmt.Errorf("resource: read: %w", e)
		}
	}
	if err = s.Check(); err != nil {
		return nil, err
	}
	if kind == Stylesheet {
		if err = s.Charge("css_bytes", int64(len(data))); err != nil {
			return nil, err
		}
	}
	if kind == Font {
		if err = s.Charge("fonts", 1); err != nil {
			return nil, err
		}
	}
	return data, nil
}

// DecodeImage reads dimensions before decoding PNG/JPEG pixels. Eight bytes per
// pixel conservatively reserve 16-bit RGBA storage. The reservation is cumulative.
func (l Loader) DecodeImage(data []byte) (image.Image, error) {
	s := l.Session
	if s == nil {
		return nil, fmt.Errorf("%w: missing session", session.ErrInvalidInput)
	}
	if err := s.CheckSize("asset_bytes", int64(len(data))); err != nil {
		return nil, err
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: image configuration: %w", session.ErrInvalidInput, err)
	}
	if format != "png" && format != "jpeg" {
		return nil, fmt.Errorf("%w: image format", session.ErrUnsupportedFeature)
	}
	width, height := int64(config.Width), int64(config.Height)
	if width <= 0 || height <= 0 || width > s.Limits.MaxImagePixels/height {
		return nil, &session.LimitError{Resource: "image_pixels", Limit: s.Limits.MaxImagePixels, Requested: s.Limits.MaxImagePixels + 1}
	}
	pixels := width * height
	if err := s.Charge("image_pixels", pixels); err != nil {
		return nil, err
	}
	if pixels > s.Limits.MaxDecodedBytes/8 {
		return nil, &session.LimitError{Resource: "decoded_bytes", Limit: s.Limits.MaxDecodedBytes, Requested: s.Limits.MaxDecodedBytes + 1}
	}
	if err := s.Charge("decoded_bytes", pixels*8); err != nil {
		return nil, err
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: decode image: %w", session.ErrInvalidInput, err)
	}
	if err := s.Check(); err != nil {
		return nil, err
	}
	return decoded, nil
}
