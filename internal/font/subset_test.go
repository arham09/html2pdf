package font

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestSubsetTimestampAndChecksums(t *testing.T) {
	face, err := Parse(bundled)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := face.Subset([]uint16{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if checksum(sub.Bytes) != 0xB1B0AFBA {
		t.Fatal("whole-font checksum")
	}
	normalized := bytes.Clone(sub.Bytes)
	for i := 0; i < int(binary.BigEndian.Uint16(normalized[4:6])); i++ {
		r := normalized[12+16*i : 28+16*i]
		if string(r[:4]) != "head" {
			continue
		}
		offset := int(binary.BigEndian.Uint32(r[8:12]))
		length := int(binary.BigEndian.Uint32(r[12:16]))
		h := normalized[offset : offset+length]
		if !bytes.Equal(h[28:36], face.sfnt.Tables["head"][28:36]) {
			t.Fatal("source timestamp lost")
		}
		binary.BigEndian.PutUint32(h[8:12], 0)
		if checksum(h) != binary.BigEndian.Uint32(r[4:8]) {
			t.Fatal("head checksum")
		}
		copy(h[28:36], []byte{0, 0, 0, 0, 1, 2, 3, 4})
	}
	again, err := stableSubset(normalized, face.sfnt.Tables["head"])
	if err != nil || !bytes.Equal(again, sub.Bytes) {
		t.Fatal("normalization did not remove timestamp differences", err)
	}
}
