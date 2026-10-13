package attachtext

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
)

// errUnzipBudget is a document that decompresses past maxUnzippedBytes.
var errUnzipBudget = errors.New("attachtext: the document expands past the size limit")

// ooxmlPackage reads the parts of a Word or Excel file, which are XML files in
// a zip, against one shared decompression budget.
type ooxmlPackage struct {
	files  map[string]*zip.File
	budget int64
}

func openOOXML(data []byte) (*ooxmlPackage, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("attachtext: not a valid Office document: %w", err)
	}
	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[f.Name] = f
	}
	return &ooxmlPackage{files: files, budget: maxUnzippedBytes}, nil
}

// names returns the parts under dir whose base name starts with prefix, in a
// stable order: sheet1, sheet2, ... sheet10, by length then name.
func (p *ooxmlPackage) names(dir, prefix string) []string {
	var out []string
	for name := range p.files {
		if path.Dir(name) == dir && strings.HasPrefix(path.Base(name), prefix) && strings.HasSuffix(name, ".xml") {
			out = append(out, name)
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		if len(a) != len(b) {
			return len(a) - len(b)
		}
		return strings.Compare(a, b)
	})
	return out
}

// open returns a reader for one part, charged against the budget. A missing
// part is not an error: a document with no footer simply has none.
func (p *ooxmlPackage) open(name string) (io.ReadCloser, bool, error) {
	f, ok := p.files[name]
	if !ok {
		return nil, false, nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil, false, err
	}
	return &budgetReader{rc: rc, pkg: p}, true, nil
}

// budgetReader fails the read once the package has decompressed more than its
// budget, whatever the zip headers claimed the sizes were.
type budgetReader struct {
	rc  io.ReadCloser
	pkg *ooxmlPackage
}

func (r *budgetReader) Read(b []byte) (int, error) {
	if r.pkg.budget <= 0 {
		return 0, errUnzipBudget
	}
	if int64(len(b)) > r.pkg.budget {
		b = b[:r.pkg.budget]
	}
	n, err := r.rc.Read(b)
	r.pkg.budget -= int64(n)
	return n, err
}

func (r *budgetReader) Close() error { return r.rc.Close() }

// extractDOCX reads the body, then headers, footers, footnotes and endnotes,
// because a confidentiality marking is as likely to sit in a header as in the
// text.
func extractDOCX(data []byte, out *limitedText) error {
	pkg, err := openOOXML(data)
	if err != nil {
		return err
	}
	parts := []string{"word/document.xml"}
	parts = append(parts, pkg.names("word", "header")...)
	parts = append(parts, pkg.names("word", "footer")...)
	parts = append(parts, "word/footnotes.xml", "word/endnotes.xml")

	found := false
	for _, name := range parts {
		rc, ok, err := pkg.open(name)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		found = true
		err = wordText(rc, out)
		rc.Close()
		if err != nil {
			return err
		}
	}
	if !found {
		return errors.New("attachtext: not a Word document")
	}
	return nil
}

// wordText walks WordprocessingML: text runs are w:t, and paragraphs, breaks
// and tabs become the whitespace a reader would see, so a phrase split across
// runs still reads as one phrase and words in separate paragraphs do not run
// together.
func wordText(r io.Reader, out *limitedText) error {
	dec := xml.NewDecoder(r)
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "tab":
				if err := out.WriteString("\t"); err != nil {
					return err
				}
			case "br", "cr":
				if err := out.WriteString("\n"); err != nil {
					return err
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				if err := out.WriteString("\n"); err != nil {
					return err
				}
			}
		case xml.CharData:
			if inText {
				if err := out.WriteString(string(t)); err != nil {
					return err
				}
			}
		}
	}
}

// extractXLSX reads the shared strings, which hold every piece of text typed
// into a cell, then each sheet for what the shared strings do not hold:
// numbers, inline strings and formula results. Numbers matter to a content
// rule, since an account or card number is a number.
func extractXLSX(data []byte, out *limitedText) error {
	pkg, err := openOOXML(data)
	if err != nil {
		return err
	}

	sheets := pkg.names("xl/worksheets", "sheet")
	rc, ok, err := pkg.open("xl/sharedStrings.xml")
	if err != nil {
		return err
	}
	if !ok && len(sheets) == 0 {
		return errors.New("attachtext: not an Excel workbook")
	}
	if ok {
		err = sharedStringsText(rc, out)
		rc.Close()
		if err != nil {
			return err
		}
	}

	for _, name := range sheets {
		rc, ok, err := pkg.open(name)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		err = sheetText(rc, out)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// sharedStringsText writes one line per shared string. A string with rich
// formatting is several t elements in one si, and is joined back together.
func sharedStringsText(r io.Reader, out *limitedText) error {
	dec := xml.NewDecoder(r)
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inText = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "si":
				if err := out.WriteString("\n"); err != nil {
					return err
				}
			}
		case xml.CharData:
			if inText {
				if err := out.WriteString(string(t)); err != nil {
					return err
				}
			}
		}
	}
}

// sheetText writes the cell values a sheet holds itself. A cell of type "s"
// is an index into the shared strings, already written, and is skipped.
func sheetText(r io.Reader, out *limitedText) error {
	dec := xml.NewDecoder(r)
	cellType := ""
	inValue := false
	cellHasText, rowHasText := false, false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "c":
				cellType = ""
				cellHasText = false
				for _, a := range t.Attr {
					if a.Name.Local == "t" {
						cellType = a.Value
					}
				}
			case "v", "t":
				inValue = cellType != "s"
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v", "t":
				inValue = false
			case "c":
				if cellHasText {
					if err := out.WriteString("\t"); err != nil {
						return err
					}
				}
			case "row":
				if rowHasText {
					if err := out.WriteString("\n"); err != nil {
						return err
					}
				}
				rowHasText = false
			}
		case xml.CharData:
			if inValue && len(t) > 0 {
				cellHasText, rowHasText = true, true
				if err := out.WriteString(string(t)); err != nil {
					return err
				}
			}
		}
	}
}
