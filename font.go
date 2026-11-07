package html2pdf

// FontFace registers static TrueType bytes for a CSS font-family. Weight is
// 400 (the default for 0) or 700. New copies Data; registration is frozen
// after construction.
type FontFace struct {
	Family string
	Weight int
	Italic bool
	Data   []byte
}
