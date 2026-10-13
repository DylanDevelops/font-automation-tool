// Package fontinfo reads the metadata the font book needs from font files.
package fontinfo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/tdewolff/font"
)

// Face is a single font face. A .ttc/.otc collection file holds several.
type Face struct {
	Path       string
	Index      int // face index within a collection, 0 otherwise
	Source     string
	Family     string
	Style      string
	FullName   string // page label
	PostScript string

	Embeddable   bool   // false if the font's license forbids embedding
	MissingLatin []rune // characters of LatinSample the font has no glyph for
	Symbol       bool   // maps Latin characters to pictures, like Webdings
	ColorBitmap  bool   // emoji font whose glyphs are bitmaps, not outlines
}

// Hidden reports whether the face is a private system UI font (".SF NS",
// ".AppleSystemUIFont", ...) that never appears in font menus.
func (f Face) Hidden() bool {
	return strings.HasPrefix(f.Family, ".") || strings.HasPrefix(f.PostScript, ".") || f.Family == "LastResort"
}

// Key identifies the face when removing duplicates across sources.
func (f Face) Key() string {
	if f.PostScript != "" {
		return f.PostScript
	}
	return f.FullName
}

// LatinSample is what a face needs for the standard specimen page.
// Punctuation is printed too but not required.
const LatinSample = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// Load returns every readable face in the font file at path, and an error
// describing any faces that could not be read.
func Load(path, source string) ([]Face, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var faces []Face
	var errs []error
	for i := range numFaces(b) {
		_, sfnt, err := Extract(b, i)
		if err != nil {
			errs = append(errs, fmt.Errorf("face %d: %w", i, err))
			continue
		}
		faces = append(faces, describe(sfnt, path, i, source))
	}
	return faces, errors.Join(errs...)
}

func describe(sfnt *font.SFNT, path string, index int, source string) Face {
	f := Face{
		Path:       path,
		Index:      index,
		Source:     source,
		Family:     name(sfnt, 16, 1), // typographic family, then legacy family
		Style:      name(sfnt, 17, 2),
		PostScript: name(sfnt, 6),
		Embeddable: true,
	}
	// Name ID 4 (full name) is only a fallback: some fonts store their
	// PostScript name there.
	f.FullName = strings.TrimSpace(f.Family + " " + f.Style)
	if strings.EqualFold(f.Style, "Regular") {
		f.FullName = f.Family
	}
	if f.Family == "" {
		f.FullName = name(sfnt, 4)
	}
	if sfnt.OS2 != nil {
		fsType := sfnt.OS2.FsType // restricted-license or bitmap-only embedding
		if fsType&0x000F == 0x0002 || fsType&0x0200 != 0 {
			f.Embeddable = false
		}
	}
	for _, r := range LatinSample {
		if sfnt.GlyphIndex(r) == 0 {
			f.MissingLatin = append(f.MissingLatin, r)
		}
	}
	if sfnt.Cmap != nil {
		for _, rec := range sfnt.Cmap.EncodingRecords {
			if rec.PlatformID == 3 && rec.EncodingID == 0 { // Windows symbol encoding
				f.Symbol = true
			}
		}
	}
	for _, tag := range []string{"sbix", "CBDT"} {
		if _, ok := sfnt.Tables[tag]; ok {
			f.ColorBitmap = true
		}
	}
	return f
}

// OwnCharacters returns up to n characters the font has glyphs for, leaving
// out combining marks, which only make sense attached to a letter.
func OwnCharacters(sfnt *font.SFNT, n int) []rune {
	seen := map[rune]bool{}
	var runes []rune
	for id := uint16(1); id < sfnt.NumGlyphs() && id < 4000; id++ {
		for _, r := range sfnt.GlyphToUnicode(id) {
			standalone := unicode.In(r, unicode.L, unicode.N, unicode.P, unicode.S, unicode.Co)
			if !seen[r] && standalone && r != '\u25CC' { // dotted circle: a placeholder for marks
				seen[r] = true
				runes = append(runes, r)
			}
		}
	}
	slices.Sort(runes)
	if len(runes) > n {
		runes = runes[:n]
	}
	return runes
}

func numFaces(b []byte) int {
	if len(b) >= 12 && string(b[:4]) == "ttcf" {
		return int(binary.BigEndian.Uint32(b[8:12]))
	}
	return 1
}

// name returns the first non-empty name table entry among ids, preferring
// English Windows records, then other Unicode records, then Mac Roman.
func name(sfnt *font.SFNT, ids ...font.NameID) string {
	if sfnt.Name == nil {
		return ""
	}
	for _, id := range ids {
		best, bestRank := "", 99
		for _, rec := range sfnt.Name.Get(id) {
			rank := 99
			switch {
			case rec.Platform == font.PlatformWindows && rec.Language == 0x0409:
				rank = 0
			case rec.Platform == font.PlatformWindows || rec.Platform == font.PlatformUnicode:
				rank = 1
			case rec.Platform == font.PlatformMacintosh && rec.Encoding == font.EncodingMacintoshRoman && rec.Language == 0:
				rank = 2
			default:
				continue // rec.String cannot decode other Mac encodings
			}
			if rank < bestRank {
				if s := strings.TrimSpace(rec.String()); s != "" {
					best, bestRank = s, rank
				}
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}
