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
	FullName   string // family + style, used as the page label
	PostScript string

	// Embeddable is false when the font's license forbids embedding it in a
	// document (OS/2 fsType restricted or bitmap-only).
	Embeddable bool
	// MissingLatin lists the specimen characters the font has no glyph for.
	MissingLatin []rune
	// Symbol is true for fonts with a Windows symbol cmap (Webdings,
	// Wingdings, ...), whose Latin code points map to pictures.
	Symbol bool
	// ColorBitmap is true for fonts whose glyphs are color bitmaps (emoji)
	// rather than outlines.
	ColorBitmap bool
}

// Hidden reports whether the face is a private system UI font (".SF NS",
// ".AppleSystemUIFont", ...) that never appears in font menus.
func (f Face) Hidden() bool {
	return strings.HasPrefix(f.Family, ".") || strings.HasPrefix(f.PostScript, ".") || f.Family == "LastResort"
}

// Key identifies the face for de-duplication across sources.
func (f Face) Key() string {
	if f.PostScript != "" {
		return f.PostScript
	}
	return f.FullName
}

// LatinSample holds the letters and digits a face needs for the standard
// specimen page. The page also prints some punctuation, but a font missing a
// symbol or two still gets the standard page.
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
	// Family + style matches how font menus present the face; name ID 4 is
	// only a fallback because some fonts store the PostScript name there.
	// "Regular" is left off, as in the reference specimen ("Myriad Pro").
	f.FullName = strings.TrimSpace(f.Family + " " + f.Style)
	if strings.EqualFold(f.Style, "Regular") {
		f.FullName = f.Family
	}
	if f.Family == "" {
		f.FullName = name(sfnt, 4)
	}
	if sfnt.OS2 != nil {
		fsType := sfnt.OS2.FsType
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
			if rec.PlatformID == 3 && rec.EncodingID == 0 {
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

// OwnCharacters returns up to n standalone characters the font does have,
// for faces that cannot show the Latin specimen (non-Latin fonts). Combining
// marks are left out because they only make sense attached to a base letter.
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

// numFaces returns the number of faces in a font file: the count from the
// header for collections, 1 otherwise.
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
				continue // other Mac encodings would need their own decoders
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
