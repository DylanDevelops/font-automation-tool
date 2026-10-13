// Command font-automation-tool builds a PDF font book with a specimen page
// for every installed font plus the fonts Adobe apps make available.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/DylanDevelops/font-automation-tool/internal/discover"
	"github.com/DylanDevelops/font-automation-tool/internal/fontinfo"
	"github.com/DylanDevelops/font-automation-tool/internal/specimen"
)

func main() {
	out := flag.String("o", "fontbook.pdf", "output PDF path")
	sources := flag.String("sources", strings.Join([]string{discover.Installed, discover.AdobeFonts, discover.AdobeApps}, ","), "comma-separated font sources to include")
	match := flag.String("match", "", "only include fonts whose name contains any of these comma-separated terms (case-insensitive)")
	limit := flag.Int("limit", 0, "maximum number of fonts to include (0 = all)")
	list := flag.Bool("list", false, "print the fonts that would be included instead of writing a PDF")
	verbose := flag.Bool("v", false, "print every font that was skipped and why")
	flag.Parse()

	if err := run(*out, *sources, *match, *limit, *list, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(out, sourceList, match string, limit int, list, verbose bool) error {
	st := newStatus()

	all, err := discover.DefaultSources()
	if err != nil {
		return err
	}
	wanted := strings.Split(sourceList, ",")
	var sources []discover.Source
	for _, s := range all {
		if slices.Contains(wanted, s.Name) {
			sources = append(sources, s)
		}
	}

	st.Update("Scanning font folders…")
	files := discover.Find(sources)
	faces, unreadable := loadFaces(files, st)

	st.Line("Found %d fonts in %d files (%s)", len(faces), len(files), st.Elapsed())
	for _, src := range sources {
		n := 0
		for _, f := range faces {
			if f.Source == src.Name {
				n++
			}
		}
		st.Line("  %-12s %4d fonts", src.Name, n)
	}
	if len(unreadable) > 0 {
		if !verbose {
			st.Line("  %d files could not be read (use -v to see why)", len(unreadable))
		} else {
			st.Line("  %d files could not be read:", len(unreadable))
			for _, msg := range unreadable {
				st.Line("    %s", strings.ReplaceAll(msg, "\n", "\n      "))
			}
		}
	}

	if match != "" {
		terms := strings.Split(strings.ToLower(match), ",")
		faces = slices.DeleteFunc(faces, func(f fontinfo.Face) bool {
			name := strings.ToLower(f.FullName)
			return !slices.ContainsFunc(terms, func(t string) bool {
				t = strings.TrimSpace(t)
				return t != "" && strings.Contains(name, t)
			})
		})
	}
	if limit > 0 && len(faces) > limit {
		faces = faces[:limit]
	}
	if match != "" || limit > 0 {
		st.Line("Selected %d fonts", len(faces))
	}

	if list {
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tSOURCE\tFILE")
		for _, f := range faces {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", f.FullName, f.Source, f.Path)
		}
		return tw.Flush()
	}

	file, err := os.Create(out)
	if err != nil {
		return err
	}
	defer file.Close()
	w := bufio.NewWriter(file)

	res, err := specimen.Write(w, faces, &renderProgress{st: st, verbose: verbose})
	if err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	size := ""
	if fi, err := os.Stat(out); err == nil {
		size = ", " + humanSize(fi.Size())
	}
	st.Line("Done in %s: wrote %s (%d pages%s)", st.Elapsed(), out, res.Pages, size)
	if n := len(res.Skipped); n > 0 && !verbose {
		st.Line("%d fonts were skipped (use -v to see why)", n)
	}
	return nil
}

// renderProgress updates the status line; with -v it also prints each
// skipped font.
type renderProgress struct {
	st      *status
	verbose bool
	skipped int
}

func (p *renderProgress) Face(done, total int, f fontinfo.Face, skipReason string) {
	if skipReason != "" {
		p.skipped++
		if p.verbose {
			p.st.Line("  skipped %s: %s", f.FullName, skipReason)
		}
	}
	skipped := ""
	if p.skipped > 0 {
		skipped = fmt.Sprintf(" · %d skipped", p.skipped)
	}
	p.st.Update("Rendering %s  %s%s", bar(done, total), truncate(f.FullName, 36), skipped)
}

func (p *renderProgress) Finishing(pages int) {
	p.st.Line("Rendered %d pages (%s)", pages, p.st.Elapsed())
	p.st.Update("Embedding fonts and writing the PDF…")
}

// loadFaces reads every file, drops hidden system faces and duplicates (the
// earliest source wins) and sorts by name.
func loadFaces(files []discover.File, st *status) ([]fontinfo.Face, []string) {
	var faces []fontinfo.Face
	var unreadable []string
	seen := map[string]bool{}
	for i, file := range files {
		st.Update("Reading fonts %s  %s", bar(i+1, len(files)), truncate(filepath.Base(file.Path), 36))
		got, err := fontinfo.Load(file.Path, file.Source)
		if err != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s: %v", file.Path, err))
		}
		for _, f := range got {
			if f.Hidden() || seen[f.Key()] {
				continue
			}
			seen[f.Key()] = true
			faces = append(faces, f)
		}
	}
	slices.SortFunc(faces, func(a, b fontinfo.Face) int {
		return strings.Compare(strings.ToLower(a.FullName), strings.ToLower(b.FullName))
	})
	return faces, unreadable
}
