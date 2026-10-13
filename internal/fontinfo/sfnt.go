package fontinfo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tdewolff/font"
)

// Extract returns one face of a font file as a standalone font, along with
// its parsed form.
//
// tdewolff/font rejects the last face of every collection and is strict
// about tables that many macOS fonts get slightly wrong, so faces are
// rebuilt from their table directory. When parsing fails on one of these
// tables, it is repaired and parsing is retried:
//
//   - kern: dropped (GPOS kerning is unaffected)
//   - post: reduced to a version 3 header, which has no glyph names
//   - cmap: reduced to its Unicode subtables, then to format 12 only
//   - head: out-of-range created/modified dates are zeroed
func Extract(b []byte, index int) ([]byte, *font.SFNT, error) {
	version, tables, err := readTables(b, index)
	if err != nil {
		return nil, nil, err
	}

	repairs := map[string]int{}
	for {
		data := writeSFNT(version, tables)
		sfnt, err := font.ParseSFNT(data, 0)
		if err == nil {
			return data, sfnt, nil
		}

		tag, _, _ := strings.Cut(err.Error(), ":")
		attempt := repairs[tag]
		repairs[tag]++
		var repairErr error
		switch {
		case tag == "kern" && attempt == 0:
			delete(tables, "kern")
		case tag == "post" && attempt == 0:
			tables["post"] = postV3(tables["post"])
		case tag == "cmap" && attempt == 0:
			tables["cmap"], repairErr = filterCmap(tables["cmap"], isUnicodeEncoding)
		case tag == "cmap" && attempt == 1:
			tables["cmap"], repairErr = filterCmap(tables["cmap"], func(_, _, format uint16) bool { return format == 12 })
		case tag == "head" && attempt == 0 && len(tables["head"]) >= 36:
			head := slices.Clone(tables["head"])
			clear(head[20:36]) // created and modified timestamps
			tables["head"] = head
		default:
			return nil, nil, err
		}
		if repairErr != nil {
			return nil, nil, repairErr
		}
	}
}

// readTables returns the sfnt version and tables of one face in b, which may
// be a single font or a collection.
func readTables(b []byte, index int) (string, map[string][]byte, error) {
	errBad := errors.New("invalid font data")
	offset := 0
	if len(b) >= 12 && string(b[:4]) == "ttcf" {
		if index < 0 || index >= numFaces(b) || len(b) < 16+4*index {
			return "", nil, fmt.Errorf("bad face index %d", index)
		}
		offset = int(binary.BigEndian.Uint32(b[12+4*index:]))
	} else if index != 0 {
		return "", nil, fmt.Errorf("bad face index %d", index)
	}
	if len(b) < offset+12 {
		return "", nil, errBad
	}

	version := string(b[offset : offset+4])
	numTables := int(binary.BigEndian.Uint16(b[offset+4:]))
	if len(b) < offset+12+16*numTables {
		return "", nil, errBad
	}
	tables := make(map[string][]byte, numTables)
	for i := range numTables {
		rec := b[offset+12+16*i:]
		tag := string(rec[:4])
		start := int(binary.BigEndian.Uint32(rec[8:]))
		length := int(binary.BigEndian.Uint32(rec[12:]))
		if start < 0 || length < 0 || len(b) < start+length {
			return "", nil, fmt.Errorf("%s: table out of bounds", strings.TrimSpace(tag))
		}
		tables[tag] = b[start : start+length : start+length]
	}
	return version, tables, nil
}

// writeSFNT writes tables out as a standalone font file.
func writeSFNT(version string, tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	size := 12 + 16*len(tables)
	for tag, data := range tables {
		tags = append(tags, tag)
		size += (len(data) + 3) &^ 3
	}
	slices.Sort(tags)

	out := make([]byte, 12+16*len(tags), size)
	copy(out, version)
	n := uint16(len(tags))
	entrySelector := uint16(0)
	for 1<<(entrySelector+1) <= n {
		entrySelector++
	}
	searchRange := uint16(16) << entrySelector
	binary.BigEndian.PutUint16(out[4:], n)
	binary.BigEndian.PutUint16(out[6:], searchRange)
	binary.BigEndian.PutUint16(out[8:], entrySelector)
	binary.BigEndian.PutUint16(out[10:], 16*n-searchRange)

	for i, tag := range tags {
		data := tables[tag]
		rec := out[12+16*i:]
		copy(rec, tag)
		binary.BigEndian.PutUint32(rec[4:], checksum(data))
		binary.BigEndian.PutUint32(rec[8:], uint32(len(out)))
		binary.BigEndian.PutUint32(rec[12:], uint32(len(data)))
		out = append(out, data...)
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
	}
	return out
}

func checksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i < len(b); i += 4 {
		var word [4]byte
		copy(word[:], b[i:])
		sum += binary.BigEndian.Uint32(word[:])
	}
	return sum
}

// postV3 keeps the metrics in a post table header and drops its glyph names.
func postV3(post []byte) []byte {
	out := make([]byte, 32)
	copy(out, post)
	binary.BigEndian.PutUint32(out, 0x00030000)
	return out
}

func isUnicodeEncoding(platform, encoding, _ uint16) bool {
	return platform == 0 || platform == 3 && (encoding == 0 || encoding == 1 || encoding == 10)
}

// filterCmap rebuilds a cmap table with only the encoding records keep
// accepts.
func filterCmap(cmap []byte, keep func(platform, encoding, format uint16) bool) ([]byte, error) {
	errBad := errors.New("cmap: bad table")
	if len(cmap) < 4 {
		return nil, errBad
	}
	numTables := int(binary.BigEndian.Uint16(cmap[2:]))
	if len(cmap) < 4+8*numTables {
		return nil, errBad
	}

	type record struct{ platform, encoding uint16 }
	var records []record
	var subtables [][]byte
	var recordSubtable []int
	written := map[uint32]int{}
	for i := range numTables {
		rec := cmap[4+8*i:]
		platform := binary.BigEndian.Uint16(rec)
		encoding := binary.BigEndian.Uint16(rec[2:])
		offset := binary.BigEndian.Uint32(rec[4:])
		sub, format, err := cmapSubtable(cmap, offset)
		if err != nil || !keep(platform, encoding, format) {
			continue
		}
		idx, ok := written[offset]
		if !ok {
			idx = len(subtables)
			written[offset] = idx
			subtables = append(subtables, sub)
		}
		records = append(records, record{platform, encoding})
		recordSubtable = append(recordSubtable, idx)
	}
	if len(records) == 0 {
		return nil, errors.New("cmap: no usable subtables")
	}

	header := 4 + 8*len(records)
	offsets := make([]uint32, len(subtables))
	out := make([]byte, header)
	binary.BigEndian.PutUint16(out[2:], uint16(len(records)))
	for i, sub := range subtables {
		offsets[i] = uint32(len(out))
		out = append(out, sub...)
	}
	for i, r := range records {
		rec := out[4+8*i:]
		binary.BigEndian.PutUint16(rec, r.platform)
		binary.BigEndian.PutUint16(rec[2:], r.encoding)
		binary.BigEndian.PutUint32(rec[4:], offsets[recordSubtable[i]])
	}
	return out, nil
}

func cmapSubtable(cmap []byte, offset uint32) ([]byte, uint16, error) {
	errBad := errors.New("cmap: bad subtable")
	if uint64(len(cmap)) < uint64(offset)+8 {
		return nil, 0, errBad
	}
	sub := cmap[offset:]
	format := binary.BigEndian.Uint16(sub)
	var length uint32
	switch format {
	case 0, 2, 4, 6:
		length = uint32(binary.BigEndian.Uint16(sub[2:]))
	case 8, 10, 12, 13:
		length = binary.BigEndian.Uint32(sub[4:])
	case 14:
		length = binary.BigEndian.Uint32(sub[2:])
	default:
		return nil, format, errBad
	}
	if uint32(len(sub)) < length {
		return nil, format, errBad
	}
	return sub[:length], format, nil
}
