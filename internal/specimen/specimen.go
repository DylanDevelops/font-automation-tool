// Package specimen renders one specimen page per font face into a PDF.
package specimen

import (
	"errors"
	"fmt"
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

// Page geometry in points (1/72 in), taken from the reference specimen: a
// US Letter Illustrator artboard. Y values are baselines measured up from
// the bottom of the page, as in the PDF itself.
const (
	pageW = 612.0
	pageH = 792.0

	marginX  = 27.0 // the rule runs from marginX to pageW-marginX
	contentW = pageW - 2*marginX

	headerX        = 77.0  // left edge of the large "Aa"
	headerBaseline = 618.0 // shared by "Aa" and the bottom line of the name
	headerMaxPt    = 200.0
	headerMaxW     = 300.0 // "Aa" shrinks for very wide fonts so the name keeps room
	nameGap        = 36.0  // space between "Aa" and the name
	nameMaxPt      = 36.0
	nameMinPt      = 14.0
	nameMaxLines   = 3

	ruleY     = 589.0
	ruleWidth = 2.0

	noteBaseline = 567.0 // explains pages that show a font's own characters
	notePt       = 11.0
)

const mmPerPt = 25.4 / 72 // canvas works in millimetres

// block is a pair of centered lines set at one size: the largest size up to
// maxPt at which both fit between the margins.
type block struct {
	lines     [2]string
	baselines [2]float64
	maxPt     float64
}

var blockBaselines = [][2]float64{{496.9, 424.9}, {280.9, 208.9}, {122.5, 79.3}}
var blockMaxPt = []float64{60, 60, 36}

var latinLines = [][2]string{
	{"ABCDEFGHIJKLMN", "OPQRSTUVWXYZ"},
	{"abcdefghijklmn", "opqrstuvwxyz"},
	{"1234567890", "!?.,@#$%^&*"},
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
		*doc = pdf.New(w, pageW*mmPerPt, pageH*mmPerPt, nil)
		(*doc).SetInfo("Font Book", "Specimens of every available font", "", "", "font-automation-tool")
	} else {
		(*doc).NewPage(pageW*mmPerPt, pageH*mmPerPt)
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

	c = canvas.New(pageW*mmPerPt, pageH*mmPerPt)
	p := &page{ctx: canvas.NewContext(c), font: fnt, used: []uint16{0}}

	if len(f.MissingLatin) == 0 {
		nameFont := fnt
		if f.Symbol {
			nameFont = fallback // a name spelled in pictures is not much use
		}
		p.header(fnt, "Aa", nameFont, f.FullName)
		for i, lines := range latinLines {
			p.block(fnt, block{lines, blockBaselines[i], blockMaxPt[i]})
		}
	} else {
		// Non-Latin fonts: name in a neutral font, then characters the font
		// actually has instead of a page of .notdef boxes.
		own := fontinfo.OwnCharacters(fnt.SFNT, 56)
		if len(own) < 2 {
			return nil, errors.New("font has no mapped characters")
		}
		p.header(fnt, string(own[:2]), fallback, f.FullName)
		note := "No Latin glyphs, showing the font's own characters"
		if len(f.MissingLatin) < len(fontinfo.LatinSample) {
			note = fmt.Sprintf("Missing %d of the specimen characters, showing the font's own characters", len(f.MissingLatin))
		}
		p.centered(fallback, note, notePt, noteBaseline)
		lines := chunk(own, 10, 10, 10, 10, 8, 8)
		for i := range blockBaselines {
			var pair [2]string
			for j := range pair {
				if k := 2*i + j; k < len(lines) {
					pair[j] = spaced(lines[k])
				}
			}
			p.block(fnt, block{pair, blockBaselines[i], blockMaxPt[i]})
		}
	}

	p.ctx.SetFillColor(canvas.Transparent)
	p.ctx.SetStrokeColor(canvas.Black)
	p.ctx.SetStrokeWidth(ruleWidth * mmPerPt)
	p.ctx.MoveTo(marginX*mmPerPt, ruleY*mmPerPt)
	p.ctx.LineTo((pageW-marginX)*mmPerPt, ruleY*mmPerPt)
	p.ctx.Stroke()

	// The PDF writer subsets fonts at Close and panics on failure, which
	// would lose the whole document. Try the same subset now instead.
	if err := checkSubset(fnt.SFNT, p.used); err != nil {
		return nil, err
	}
	return c, nil
}

// page draws onto one specimen page, working in points, and records which
// glyphs of the specimen font were used.
type page struct {
	ctx  *canvas.Context
	font *canvas.Font
	used []uint16
}

// text draws s with its left edge at x and its baseline at y.
func (p *page) text(font *canvas.Font, s string, size, x, y float64) {
	face := font.Face(size, canvas.Black)
	p.ctx.DrawText(x*mmPerPt, y*mmPerPt, canvas.NewTextLine(face, s, canvas.Left))
	if font == p.font {
		for _, g := range face.Glyphs(s) {
			p.used = append(p.used, g.ID)
		}
	}
}

// centered draws s centered on the page with its baseline at y.
func (p *page) centered(font *canvas.Font, s string, size, y float64) {
	p.text(font, s, size, (pageW-textWidth(font, s, size))/2, y)
}

// header draws the large sample (normally "Aa") and, beside it, the font
// name. Long names wrap upward from the shared baseline and shrink if three
// lines are not enough.
func (p *page) header(font *canvas.Font, sample string, nameFont *canvas.Font, name string) {
	size := fitSize(font, sample, headerMaxPt, headerMaxW)
	p.text(font, sample, size, headerX, headerBaseline)

	x := headerX + textWidth(font, sample, size) + nameGap
	avail := pageW - marginX - x
	for size := nameMaxPt; size >= nameMinPt; size-- {
		if lines, ok := wrap(nameFont, name, size, avail); ok && len(lines) <= nameMaxLines {
			for i, line := range lines {
				above := float64(len(lines) - 1 - i)
				p.text(nameFont, line, size, x, headerBaseline+above*1.2*size)
			}
			return
		}
	}
	// Still too long: one line, as large as fits.
	p.text(nameFont, name, fitSize(nameFont, name, nameMinPt, avail), x, headerBaseline)
}

// block draws a pair of centered lines at a shared size.
func (p *page) block(font *canvas.Font, b block) {
	size := b.maxPt
	for _, line := range b.lines {
		if line != "" {
			size = min(size, fitSize(font, line, b.maxPt, contentW))
		}
	}
	for i, line := range b.lines {
		if line != "" {
			p.centered(font, line, size, b.baselines[i])
		}
	}
}

// textWidth returns the advance width of s in points.
func textWidth(font *canvas.Font, s string, size float64) float64 {
	return font.Face(size, canvas.Black).TextWidth(s) / mmPerPt
}

// fitSize returns the largest point size up to maxPt at which s is no wider
// than width points.
func fitSize(font *canvas.Font, s string, maxPt, width float64) float64 {
	const probe = 100.0
	w := textWidth(font, s, probe)
	if w <= 0 {
		return maxPt
	}
	return math.Min(maxPt, probe*width/w)
}

// wrap breaks s at spaces into lines no wider than width points. ok is
// false if a single word is wider than width.
func wrap(font *canvas.Font, s string, size, width float64) (lines []string, ok bool) {
	for _, word := range strings.Fields(s) {
		if n := len(lines); n > 0 && textWidth(font, lines[n-1]+" "+word, size) <= width {
			lines[n-1] += " " + word
			continue
		}
		if textWidth(font, word, size) > width {
			return nil, false
		}
		lines = append(lines, word)
	}
	return lines, true
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
