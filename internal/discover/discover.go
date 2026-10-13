// Package discover finds font files on disk, including the ones Adobe apps
// see but the operating system does not list.
package discover

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Source is a named group of directories that hold font files.
type Source struct {
	Name string
	Dirs []string
}

// File is a font file found on disk and the source it came from.
type File struct {
	Path   string
	Source string
}

// Source names, in the order they are scanned. When the same font exists in
// more than one source, the earlier source wins.
const (
	Installed  = "installed"   // fonts installed in the OS
	AdobeFonts = "adobe-fonts" // fonts activated through Adobe Fonts (Creative Cloud)
	AdobeApps  = "adobe-apps"  // fonts bundled inside Adobe app packages
)

var fontExts = map[string]bool{".otf": true, ".ttf": true, ".ttc": true, ".otc": true}

// DefaultSources returns the font locations for the current platform.
func DefaultSources() ([]Source, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("only macOS is supported for now")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	// Adobe app bundles carry private fonts (e.g. Myriad, Minion, Acumin
	// variable concepts) that only show up in Adobe font menus.
	appFonts, _ := filepath.Glob("/Applications/Adobe */*.app/Contents/Required/Fonts")
	sort.Sort(sort.Reverse(sort.StringSlice(appFonts))) // newest app version first

	return []Source{
		{Name: Installed, Dirs: []string{
			filepath.Join(home, "Library/Fonts"),
			"/Library/Fonts",
			"/System/Library/Fonts",
		}},
		// Creative Cloud syncs activated Adobe Fonts here as hidden files with
		// numeric names (e.g. .w/.1344.otf). They are plain OpenType fonts.
		{Name: AdobeFonts, Dirs: []string{
			filepath.Join(home, "Library/Application Support/Adobe/CoreSync/plugins/livetype"),
		}},
		{Name: AdobeApps, Dirs: appFonts},
	}, nil
}

// Find walks every directory in sources and returns the font files it finds.
// Missing directories are skipped silently.
func Find(sources []Source) []File {
	var files []File
	seen := map[string]bool{}
	for _, src := range sources {
		for _, dir := range src.Dirs {
			filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					if d != nil && d.IsDir() {
						return fs.SkipDir
					}
					return nil
				}
				if d.IsDir() || !fontExts[strings.ToLower(filepath.Ext(path))] || seen[path] {
					return nil
				}
				seen[path] = true
				files = append(files, File{Path: path, Source: src.Name})
				return nil
			})
		}
	}
	return files
}
