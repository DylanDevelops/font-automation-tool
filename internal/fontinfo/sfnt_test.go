package fontinfo

import (
	"os"
	"testing"
)

// These tests use fonts that ship with macOS and are skipped elsewhere.

func readSystemFont(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("system font not available: %v", err)
	}
	return b
}

func TestExtractEveryCollectionFace(t *testing.T) {
	// tdewolff/font alone rejects the last face of a collection.
	b := readSystemFont(t, "/System/Library/Fonts/Avenir.ttc")
	n := numFaces(b)
	if n < 2 {
		t.Fatalf("expected a collection, got %d faces", n)
	}
	for i := range n {
		if _, _, err := Extract(b, i); err != nil {
			t.Errorf("face %d: %v", i, err)
		}
	}
}

func TestExtractRepairsKernTable(t *testing.T) {
	// Helvetica's legacy kern table fails strict parsing.
	b := readSystemFont(t, "/System/Library/Fonts/Helvetica.ttc")
	_, sfnt, err := Extract(b, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sfnt.GlyphIndex('A') == 0 {
		t.Error("repaired font lost its cmap")
	}
}

func TestLoadNamesFromFamilyAndStyle(t *testing.T) {
	faces, err := Load("/System/Library/Fonts/Supplemental/Arial Bold.ttf", "test")
	if os.IsNotExist(err) {
		t.Skip("system font not available")
	}
	if err != nil {
		t.Fatal(err)
	}
	if got, want := faces[0].FullName, "Arial Bold"; got != want {
		t.Errorf("FullName = %q, want %q", got, want)
	}
	if len(faces[0].MissingLatin) != 0 {
		t.Errorf("Arial reported missing Latin glyphs: %q", string(faces[0].MissingLatin))
	}
}
