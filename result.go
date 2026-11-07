package html2pdf

import "github.com/arham09/html2pdf/internal/session"

// Warning reports input that the engine ignored or did not apply exactly, such
// as unsupported CSS in permissive mode.
type Warning struct {
	Code     string // stable identifier, such as "unsupported-element"
	Message  string // human-readable description
	SourceID uint64 // the related source node, numbered in parse order
}

func publicWarnings(internal []session.Warning) []Warning {
	if len(internal) == 0 {
		return nil
	}
	out := make([]Warning, len(internal))
	for i, w := range internal {
		out[i] = Warning(w)
	}
	return out
}

// Result describes a render. Warnings are set also when Render fails.
type Result struct {
	Pages    int
	Warnings []Warning
}
