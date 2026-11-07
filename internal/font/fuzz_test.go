package font

import "testing"

func FuzzStaticFont(f *testing.F) {
	f.Add([]byte("not a font"))
	f.Add([]byte{0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add(bundled)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		face, err := Parse(data)
		if err != nil {
			return
		}
		run, err := face.Shape("café é 1.234,56", 10)
		if err != nil {
			return
		}
		ids := make([]uint16, len(run.Glyphs))
		for i, g := range run.Glyphs {
			ids[i] = g.ID
		}
		_, _ = face.Subset(ids)
	})
}
