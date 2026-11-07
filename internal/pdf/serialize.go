package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/arham09/html2pdf/internal/paint"
	"github.com/arham09/html2pdf/internal/session"
	"github.com/benoitkugler/pdf/model"
)

// objects writes only the profile prepared by encodeFont/imageObject: static
// Type0/CID TrueType, RGB images/alpha, and already-positioned page content.
// It does not accept arbitrary PDF objects or parse caller PDF data.
type objects struct {
	s     *session.Session
	items [][]byte
	size  int64
	err   error
}

func (o *objects) reserve() int { o.items = append(o.items, nil); return len(o.items) }
func (o *objects) set(id int, data []byte) {
	if o.err != nil {
		return
	}
	if err := o.s.Charge("layout_work", 1); err != nil {
		o.err = err
		return
	}
	if int64(len(data)) > o.s.Limits.MaxOutputBytes-o.size {
		o.err = &session.LimitError{Resource: "output_bytes", Limit: o.s.Limits.MaxOutputBytes, Used: o.size, Requested: int64(len(data))}
		return
	}
	o.size += int64(len(data))
	o.items[id-1] = data
}
func (o *objects) add(text string) int { id := o.reserve(); o.set(id, []byte(text)); return id }
func (o *objects) stream(fields string, data []byte) int {
	id := o.reserve()
	if o.err != nil {
		return id
	}
	n := int64(len(data)) + int64(len(fields)) + 80
	if n > o.s.Limits.MaxOutputBytes-o.size {
		o.err = &session.LimitError{Resource: "output_bytes", Limit: o.s.Limits.MaxOutputBytes, Used: o.size, Requested: n}
		return id
	}
	var b bytes.Buffer // bytes.Buffer writes cannot fail.
	fmt.Fprintf(&b, "<< /Length %d %s >>\nstream\n", len(data), fields)
	b.Write(data)
	b.WriteString("\nendstream")
	o.set(id, b.Bytes())
	return id
}
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeDeterministic(s *session.Session, dst io.Writer, pages []paint.Page, contents []string, fonts map[string]encodedFont, fontNames map[string]model.Name, images map[string]*model.XObjectImage, imageNames map[string]model.Name) error {
	o := objects{s: s}
	catalog, tree, resources, info := o.reserve(), o.reserve(), o.reserve(), o.reserve()
	var fontResources, imageResources strings.Builder
	for _, key := range sortedKeys(fonts) {
		dictionary := fonts[key].dictionary
		f, ok := dictionary.Subtype.(model.FontType0)
		if !ok {
			return fmt.Errorf("%w: unsupported prepared font", session.ErrInvalidInput)
		}
		cid := f.DescendantFonts
		d := cid.FontDescriptor
		mapping, ok := cid.CIDToGIDMap.(model.CIDToGIDMapStream)
		if !ok {
			return fmt.Errorf("%w: missing prepared glyph map", session.ErrInvalidInput)
		}
		widths, ok := cid.W[0].(model.CIDWidthArray)
		if !ok {
			return fmt.Errorf("%w: missing prepared widths", session.ErrInvalidInput)
		}
		file := o.stream(fmt.Sprintf("/Filter /FlateDecode /Length1 %d", d.FontFile.Length1), d.FontFile.Content)
		box := d.FontBBox
		descriptor := o.add(fmt.Sprintf("<< /Type /FontDescriptor /FontName %s /Flags %d /FontBBox [%.6f %.6f %.6f %.6f] /ItalicAngle %.6f /Ascent %.6f /Descent %.6f /CapHeight %.6f /StemV %.6f /FontFile2 %d 0 R >>", d.FontName.String(), d.Flags, box.Llx, box.Lly, box.Urx, box.Ury, d.ItalicAngle, d.Ascent, d.Descent, d.CapHeight, d.StemV, file))
		gid := o.stream("/Filter /FlateDecode", mapping.Content)
		var w strings.Builder
		for _, value := range widths.W {
			fmt.Fprintf(&w, "%.6f ", value)
		}
		descendant := o.add(fmt.Sprintf("<< /Type /Font /Subtype /CIDFontType2 /BaseFont %s /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /FontDescriptor %d 0 R /DW 1000 /W [1 [%s]] /CIDToGIDMap %d 0 R >>", f.BaseFont.String(), descriptor, w.String(), gid))
		unicode := o.stream("/Filter /FlateDecode", deflate(dictionary.ToUnicode.Content))
		font := o.add(fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont %s /Encoding /Identity-H /DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>", f.BaseFont.String(), descendant, unicode))
		fmt.Fprintf(&fontResources, "%s %d 0 R ", fontNames[key].String(), font)
	}
	for _, key := range sortedKeys(images) {
		img := images[key]
		mask := ""
		if img.SMask != nil {
			alpha := o.stream(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /BitsPerComponent 8 /ColorSpace /DeviceGray /Filter /FlateDecode", img.Width, img.Height), img.SMask.Content)
			mask = fmt.Sprintf("/SMask %d 0 R", alpha)
		}
		image := o.stream(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /BitsPerComponent 8 /ColorSpace /DeviceRGB /Filter /FlateDecode %s", img.Width, img.Height, mask), img.Content)
		fmt.Fprintf(&imageResources, "%s %d 0 R ", imageNames[key].String(), image)
	}
	o.set(resources, []byte(fmt.Sprintf("<< /Font << %s >> /XObject << %s >> >>", fontResources.String(), imageResources.String())))
	var kids strings.Builder
	for i, page := range pages {
		content := o.stream("/Filter /FlateDecode", deflate([]byte(contents[i])))
		p := o.add(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %.6f %.6f] /Resources %d 0 R /Contents %d 0 R >>", tree, page.WidthPt, page.HeightPt, resources, content))
		fmt.Fprintf(&kids, "%d 0 R ", p)
	}
	o.set(tree, []byte(fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(pages), kids.String())))
	o.set(catalog, []byte(fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", tree)))
	o.set(info, []byte("<< /Producer (html2pdf) /Title (Business document) >>"))
	if o.err != nil {
		return o.err
	}
	// Precompute all offsets and final size before touching the caller writer.
	header := []byte("%PDF-1.7\n%\xc8\xc8\xc8\xc8\n")
	chunks := [][]byte{header}
	offsets := make([]int64, len(o.items))
	position := int64(len(header))
	for i, item := range o.items {
		if err := s.Charge("layout_work", 1); err != nil {
			return err
		}
		if item == nil {
			return fmt.Errorf("%w: incomplete PDF object", session.ErrInvalidInput)
		}
		offsets[i] = position
		start := []byte(strconv.Itoa(i+1) + " 0 obj\n")
		end := []byte("\nendobj\n")
		chunks = append(chunks, start, item, end)
		position += int64(len(start) + len(item) + len(end))
	}
	var tail bytes.Buffer
	fmt.Fprintf(&tail, "xref\n0 %d\n0000000000 65535 f \n", len(o.items)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&tail, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&tail, "trailer\n<< /Size %d /Root %d 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(o.items)+1, catalog, info, position)
	position += int64(tail.Len())
	if err := s.CheckSize("output_bytes", position); err != nil {
		return err
	}
	chunks = append(chunks, tail.Bytes())
	writer := session.Writer{Session: s, Destination: dst}
	for _, chunk := range chunks {
		if _, err := writer.Write(chunk); err != nil {
			return fmt.Errorf("pdf output: %w", err)
		}
	}
	return s.Check()
}

// deflate compresses a stream for /FlateDecode. The output is deterministic
// for one Go version.
func deflate(data []byte) []byte {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(data) // bytes.Buffer writes cannot fail.
	w.Close()
	return b.Bytes()
}
