package resource

import (
	"context"
	"github.com/arham09/html2pdf/internal/session"
	"testing"
)

func FuzzImageAndDataURL(f *testing.F) {
	for _, seed := range [][]byte{nil, []byte("data:,abc"), []byte("data:;base64,AAAA"), []byte("\x89PNG\r\n\x1a\n"), []byte("\xff\xd8\xff")} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 8192 {
			return
		}
		s, _ := session.New(context.Background(), session.Limits{MaxAssetBytes: 8192, MaxResourceURIBytes: 8192, MaxImagePixels: 4096, MaxDecodedBytes: 65536})
		l := Loader{Session: s}
		_, _ = l.DecodeImage(data)
		_, _ = l.Load(Image, string(data))
	})
}
