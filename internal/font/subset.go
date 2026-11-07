package font

import (
	"encoding/binary"
	"fmt"
	"sort"

	sfnt "github.com/tdewolff/font"
)

// Subset contains the remapping from original glyph IDs to subset glyph IDs.
type Subset struct {
	Bytes                        []byte
	GlyphIDs                     map[uint16]uint16
	OriginalGlyphs, SubsetGlyphs int
}

func (f *Font) Subset(ids []uint16) (Subset, error) {
	seen := map[uint16]bool{0: true}
	for _, id := range ids {
		if id >= f.sfnt.NumGlyphs() {
			return Subset{}, fmt.Errorf("font: glyph %d out of range", id)
		}
		seen[id] = true
	}
	ordered := make([]uint16, 0, len(seen))
	for id := range seen {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	remap := make(map[uint16]uint16, len(ordered))
	for i, id := range ordered {
		remap[id] = uint16(i)
	}
	sub, err := f.sfnt.Subset(ordered, sfnt.SubsetOptions{Tables: sfnt.KeepMinTables})
	if err != nil {
		return Subset{}, fmt.Errorf("font: subset: %w", err)
	}
	data, err := stableSubset(sub.Write(), f.sfnt.Tables["head"])
	if err != nil {
		return Subset{}, err
	}
	return Subset{Bytes: data, GlyphIDs: remap, OriginalGlyphs: int(f.sfnt.NumGlyphs()), SubsetGlyphs: int(sub.NumGlyphs())}, nil
}

// The subsetter writes the current time into head.modified. Retain the source
// font's value and repair both the head-table and complete-font checksums.
// data is the private subset output, never caller-owned input.
func stableSubset(data, sourceHead []byte) ([]byte, error) {
	if len(data) < 12 || len(sourceHead) < 36 {
		return nil, fmt.Errorf("font: incomplete subset header")
	}
	count := int(binary.BigEndian.Uint16(data[4:6]))
	if count > (len(data)-12)/16 {
		return nil, fmt.Errorf("font: invalid subset directory")
	}
	for i := 0; i < count; i++ {
		record := data[12+16*i : 28+16*i]
		if string(record[:4]) != "head" {
			continue
		}
		start := int64(binary.BigEndian.Uint32(record[8:12]))
		length := int64(binary.BigEndian.Uint32(record[12:16]))
		if length < 36 || start > int64(len(data))-length {
			return nil, fmt.Errorf("font: invalid subset head")
		}
		head := data[start : start+length]
		copy(head[28:36], sourceHead[28:36])
		binary.BigEndian.PutUint32(head[8:12], 0)
		binary.BigEndian.PutUint32(record[4:8], checksum(head))
		binary.BigEndian.PutUint32(head[8:12], 0xB1B0AFBA-checksum(data))
		return data, nil
	}
	return nil, fmt.Errorf("font: subset has no head table")
}
func checksum(data []byte) uint32 {
	var sum uint32
	for len(data) >= 4 {
		sum += binary.BigEndian.Uint32(data[:4])
		data = data[4:]
	}
	if len(data) > 0 {
		var tail [4]byte
		copy(tail[:], data)
		sum += binary.BigEndian.Uint32(tail[:])
	}
	return sum
}
