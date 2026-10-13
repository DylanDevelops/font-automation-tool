# font-automation-tool
A very simple go command line tool made for Allie! :)

It builds a PDF "font book" with one specimen page per font: the font's name, a
pangram, and the alphabet and numerals, all set in that font. It includes every
font installed on the Mac **plus** the fonts that only Adobe apps can see:

- **Installed fonts:** `~/Library/Fonts`, `/Library/Fonts`, `/System/Library/Fonts`
- **Adobe Fonts** activated through Creative Cloud. These are synced as hidden
  files under `~/Library/Application Support/Adobe/CoreSync/plugins/livetype/`.
- **Adobe app fonts** bundled inside the apps, e.g. Myriad Pro in
  `Adobe Illustrator.app/Contents/Required/Fonts`

See [`samples/fontbook-sample.pdf`](samples/fontbook-sample.pdf) for example output.

## Usage

```sh
go run .                          # writes fontbook.pdf with every font
go run . -o ~/Desktop/fonts.pdf   # choose the output fi le
go run . -list                    # just list the fonts that would be included
go run . -match "bebas,franklin"  # only fonts whose names contain these terms
go run . -sources adobe-fonts     # only Adobe Fonts (installed, adobe-fonts, adobe-apps)
go run . -v                       # explain every font that was skipped
```

## Known limitations

- macOS only for now.
- Some fonts are skipped and reported (use `-v`):
  - fonts whose license forbids embedding
  - color emoji fonts
  - variable fonts in the CFF2 format, including Illustrator's "Variable Concept"
    fonts and SF India
  - several Japanese, Chinese and Korean system fonts the PDF library cannot
    subset yet (Hiragino, Apple SD Gothic Neo, ...)
- Variable fonts show only their default style, not every named style.
- Zapf Dingbats renders as a blank page.
- Adobe Fonts only appear once Creative Cloud has synced them to this Mac.
