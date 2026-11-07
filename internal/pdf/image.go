package pdf

import (
	"image"
	"image/color"

	"github.com/arham09/html2pdf/internal/session"
	"github.com/benoitkugler/pdf/model"
)

func imageObject(s *session.Session, img image.Image) (*model.XObjectImage, error) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	pixels := int64(w) * int64(h)
	if pixels > s.Limits.MaxDecodedBytes/4 {
		return nil, &session.LimitError{Resource: "decoded_bytes", Limit: s.Limits.MaxDecodedBytes, Requested: s.Limits.MaxDecodedBytes + 1}
	}
	if err := s.Charge("decoded_bytes", pixels*4); err != nil {
		return nil, err
	}
	rgb := make([]byte, w*h*3)
	alpha := make([]byte, w*h)
	transparent := false
	for y := 0; y < h; y++ {
		if err := s.Charge("layout_work", int64(w)); err != nil {
			return nil, err
		}
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA)
			i := y*w + x
			rgb[i*3], rgb[i*3+1], rgb[i*3+2], alpha[i] = c.R, c.G, c.B, c.A
			transparent = transparent || c.A != 255
		}
	}
	obj := &model.XObjectImage{Image: model.Image{Stream: model.NewCompressedStream(rgb), BitsPerComponent: 8, Width: w, Height: h}, ColorSpace: model.ColorSpaceRGB}
	if transparent {
		obj.SMask = &model.ImageSMask{Image: model.Image{Stream: model.NewCompressedStream(alpha), BitsPerComponent: 8, Width: w, Height: h}}
	}
	return obj, nil
}
