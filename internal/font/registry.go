package font

import (
	"fmt"
	"github.com/arham09/html2pdf/internal/session"
	"strings"
)

type FaceSpec struct {
	Family string
	Weight int
	Italic bool
	Data   []byte
}
type Registry struct {
	s      *session.Session
	custom []FaceSpec
	Faces  map[string]*Font
}

func NewRegistry(s *session.Session, custom []FaceSpec) *Registry {
	return &Registry{s: s, custom: custom, Faces: make(map[string]*Font)}
}

// Resolve loads mutable font state once per request. Custom bytes/counts are
// charged by Engine.start; embedded faces are charged lazily here.
func (r *Registry) Resolve(families string, weight int, slant bool) (string, *Font, error) {
	if err := r.s.Check(); err != nil {
		return "", nil, err
	}
	for _, name := range strings.Split(families, ",") {
		name = strings.Trim(strings.TrimSpace(name), "\"'")
		key := ""
		var data []byte
		embedded := false
		for i, face := range r.custom {
			if strings.EqualFold(strings.TrimSpace(face.Family), name) && face.Weight == weight && face.Italic == slant {
				key = fmt.Sprintf("custom-%d", i)
				data = face.Data
				break
			}
		}
		if key == "" && (strings.EqualFold(name, "Noto Sans") || strings.EqualFold(name, "sans-serif")) {
			key = fmt.Sprintf("noto-%d-%t", weight, slant)
			data = bundled
			embedded = true
			if weight == 700 && slant {
				data = boldItalic
			} else if weight == 700 {
				data = bold
			} else if slant {
				data = italic
			}
		}
		if key == "" {
			continue
		}
		if f := r.Faces[key]; f != nil {
			return key, f, nil
		}
		if err := r.s.CheckSize("font_bytes", int64(len(data))); err != nil {
			return "", nil, err
		}
		if embedded {
			if err := r.s.Charge("fonts", 1); err != nil {
				return "", nil, err
			}
			if err := r.s.Charge("total_asset_bytes", int64(len(data))); err != nil {
				return "", nil, err
			}
		}
		f, err := Parse(data)
		if err != nil {
			return "", nil, fmt.Errorf("%w: font parsing: %w", session.ErrUnsupportedFeature, err)
		}
		r.Faces[key] = f
		return key, f, nil
	}
	return "", nil, fmt.Errorf("%w: requested font family/style is not registered", session.ErrMissingResource)
}
func (r *Registry) Shape(text, family string, weight int, slant bool, size float64) (string, Run, error) {
	if err := r.s.Charge("layout_work", int64(len(text))+1); err != nil {
		return "", Run{}, err
	}
	id, f, err := r.Resolve(family, weight, slant)
	if err != nil {
		return "", Run{}, err
	}
	run, err := f.Shape(text, size)
	if err != nil {
		return "", Run{}, fmt.Errorf("%w: shape: %w", session.ErrUnsupportedFeature, err)
	}
	if err := r.s.Charge("glyphs", int64(len(run.Glyphs))); err != nil {
		return "", Run{}, err
	}
	return id, run, nil
}
