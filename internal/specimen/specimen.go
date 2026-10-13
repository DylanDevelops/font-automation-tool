// Package specimen renders one specimen page per font face into a PDF.
package specimen

import (
	"errors"
	"fmt"
	"image/color"
	"io"
	"math"
	"os"
	"strings"

	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers/pdf"
	tfont "github.com/tdewolff/font"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/DylanDevelops/font-automation-tool/internal/fontinfo"
)

// Page geometry in millimetres, measured from the reference specimen
// (a 6 × 3 in Illustrator artboard). Baselines are measured from the top.
const (
	pageW    = 152.4
	pageH    = 76.2
	margin   = 2.5
	contentW = pageW - 2*margin

	labelBaseline = 5.1
	labelMaxPt    = 10.0

	pangramBaseline = 14.5
	pangramMaxPt    = 24.0

	ruleY     = 18.5
	ruleWidth = 0.6

	gridFirstBaseline = 29.7
	gridStep          = 13.4
	gridMaxPt         = 32.0
)

const pangram = "The quick brown fox jumps over the lazy dog"

var gridRows = []string{
	"A a B b C c D d E e F f G g H h I i",
	"J j K k L l M m N n O o P p Q q R r",
	"S s T t U u V v W w X x Y y Z z",
	"1 2 3 4 5 6 7 8 9",
}

// Skip records a face that could not be rendered and why.
type Skip struct {
	Face   fontinfo.Face
	Reason string
}

// Result summarises a Write call.
type Result struct {
	Pages   int
	Skipped []Skip
}

// Progress receives status updates from Write.
type Progress interface {
	// Face is called after each face is rendered or skipped. skipReason is
	// empty when the face got a page.
	Face(done, total int, f fontinfo.Face, skipReason string)
	// Finishing is called once every page is laid out, before fonts are
	// subset and embedded and the PDF is written out.
	Finishing(pages int)
}

// Write renders a page for each face to w, reporting to progress if it is
// non-nil.
func Write(w io.Writer, faces []fontinfo.Face, progress Progress) (Result, error) {
	var res Result
	fallback, err := canvas.LoadFont(goregular.TTF, 0, canvas.FontRegular)
	if err != nil {
		return res, err
	}

	// Font files are cached because collections hold many faces, and the PDF
	// writer keeps every font alive until Close anyway.
	files := map[string][]byte{}
	var doc *pdf.PDF
	for i, f := range faces {
		reason := ""
		if err := renderFace(&doc, w, f, files, fallback); err != nil {
			reason = err.Error()
			res.Skipped = append(res.Skipped, Skip{f, reason})
		} else {
			res.Pages++
		}
		if progress != nil {
			progress.Face(i+1, len(faces), f, reason)
		}
	}
	if doc == nil {
		return res, errors.New("no fonts could be rendered")
	}
	if progress != nil {
		progress.Finishing(res.Pages)
	}
	return res, doc.Close()
}

// renderFace adds a page for f to *doc, creating the document on the first
// successful page. It returns why the face was skipped, if it was.
func renderFace(doc **pdf.PDF, w io.Writer, f fontinfo.Face, files map[string][]byte, fallback *canvas.Font) error {
	if !f.Embeddable {
		return errors.New("license does not allow embedding")
	}
	if f.ColorBitmap {
		return errors.New("color bitmap (emoji) fonts are not supported yet")
	}
	data, ok := files[f.Path]
	if !ok {
		var err error
		if data, err = os.ReadFile(f.Path); err != nil {
			return err
		}
		files[f.Path] = data
	}

	page, err := buildPage(f, data, fallback)
	if err != nil {
		return err
	}
	if *doc == nil {
		*doc = pdf.New(w, pageW, pageH, nil)
		(*doc).SetInfo("Font Book", "Specimens of every available font", "", "", "font-automation-tool")
	} else {
		(*doc).NewPage(pageW, pageH)
	}
	page.RenderTo(*doc)
	return nil
}

// buildPage lays out one specimen on its own canvas so that a font that
// panics inside the shaper or subsetter can be skipped without leaving a
// half-drawn page in the PDF.
func buildPage(f fontinfo.Face, data []byte, fallback *canvas.Font) (c *canvas.Canvas, err error) {
	defer func() {
		if r := recover(); r != nil {
			c, err = nil, fmt.Errorf("render failed: %v", r)
		}
	}()

	standalone, _, err := fontinfo.Extract(data, f.Index)
	if err != nil {
		return nil, err
	}
	fnt, err := canvas.LoadFont(standalone, 0, canvas.FontRegular)
	if err != nil {
		return nil, err
	}

	c = canvas.New(pageW, pageH)
	ctx := canvas.NewContext(c)
	used := []uint16{0}
	draw := func(font *canvas.Font, s string, maxPt, baseline float64, col color.Color) {
		face := font.Face(fitSize(font, s, maxPt), col)
		ctx.DrawText(margin, pageH-baseline, canvas.NewTextLine(face, s, canvas.Left))
		if font == fnt {
			for _, g := range face.Glyphs(s) {
				used = append(used, g.ID)
			}
		}
	}

	if len(f.MissingLatin) == 0 {
		labelFont := fnt
		if f.Symbol {
			labelFont = fallback // a name spelled in pictures is not much use
		}
		draw(labelFont, f.FullName, labelMaxPt, labelBaseline, canvas.Black)
		draw(fnt, pangram, pangramMaxPt, pangramBaseline, canvas.Black)
		for i, row := range gridRows {
			draw(fnt, row, gridMaxPt, gridFirstBaseline+float64(i)*gridStep, canvas.Black)
		}
	} else {
		// Symbol and non-Latin fonts: label in a neutral font, then show
		// characters the font actually has instead of a wall of .notdef boxes.
		own := fontinfo.OwnCharacters(fnt.SFNT, 44)
		if len(own) == 0 {
			return nil, errors.New("font has no mapped characters")
		}
		note := fmt.Sprintf("%s  ·  no Latin glyphs, showing the font's own characters", f.FullName)
		if len(f.MissingLatin) < len(fontinfo.LatinSample) {
			note = fmt.Sprintf("%s  ·  missing %d specimen characters, showing the font's own characters", f.FullName, len(f.MissingLatin))
		}
		draw(fallback, note, labelMaxPt, labelBaseline, canvas.Black)
		lines := chunk(own, 12, 8, 8, 8, 8)
		draw(fnt, spaced(lines[0]), pangramMaxPt, pangramBaseline, canvas.Black)
		for i, line := range lines[1:] {
			draw(fnt, spaced(line), gridMaxPt, gridFirstBaseline+float64(i)*gridStep, canvas.Black)
		}
	}

	ctx.SetFillColor(canvas.Transparent)
	ctx.SetStrokeColor(canvas.Black)
	ctx.SetStrokeWidth(ruleWidth)
	ctx.MoveTo(margin, pageH-ruleY)
	ctx.LineTo(pageW-margin, pageH-ruleY)
	ctx.Stroke()

	// The PDF writer subsets fonts at Close and panics on failure, which
	// would lose the whole document. Try the same subset now instead.
	if err := checkSubset(fnt.SFNT, used); err != nil {
		return nil, err
	}
	return c, nil
}

// fitSize returns the largest point size up to maxPt at which s fits the
// content width.
func fitSize(font *canvas.Font, s string, maxPt float64) float64 {
	const probe = 100.0
	w := font.Face(probe, canvas.Black).TextWidth(s)
	if w <= 0 {
		return maxPt
	}
	return math.Min(maxPt, probe*contentW/w)
}

func checkSubset(sfnt *tfont.SFNT, glyphIDs []uint16) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("font subsetting failed: %v", r)
		}
	}()
	seen := map[uint16]bool{}
	ids := glyphIDs[:0:0]
	for _, id := range glyphIDs {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if sfnt.IsCFF && sfnt.CFF != nil {
		sfnt.CFF.SetGlyphNames(nil) // mirrors what the PDF writer does
	}
	sub, err := sfnt.Subset(ids, tfont.SubsetOptions{Tables: tfont.KeepPDFTables})
	if err != nil {
		return fmt.Errorf("font subsetting failed: %w", err)
	}
	sub.Write()
	return nil
}

// chunk splits runes into consecutive groups of the given sizes, dropping
// empty groups at the end.
func chunk(runes []rune, sizes ...int) [][]rune {
	var out [][]rune
	for _, n := range sizes {
		if len(runes) == 0 {
			break
		}
		n = min(n, len(runes))
		out = append(out, runes[:n])
		runes = runes[n:]
	}
	return out
}

func spaced(runes []rune) string {
	parts := make([]string, len(runes))
	for i, r := range runes {
		parts[i] = string(r)
	}
	return strings.Join(parts, " ")
}
