package attachtext

import (
	"bytes"
	"fmt"

	"github.com/ledongthuc/pdf"
)

// extractPDF reads the text layer of each page, up to maxPDFPages.
//
// A scanned PDF is images and has no text layer, so it yields nothing; an
// encrypted one cannot be opened without its password and returns an error.
// Neither is guessed at.
func extractPDF(data []byte, out *limitedText) error {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("attachtext: not a readable PDF: %w", err)
	}

	pages := min(r.NumPage(), maxPDFPages)
	for i := 1; i <= pages; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		// nil fonts: the library caches per page. Font names such as F1 are
		// local to a page, so a cache shared across pages would decode one
		// page's text with another page's font.
		text, err := page.GetPlainText(nil)
		if err != nil {
			return fmt.Errorf("attachtext: page %d could not be read: %w", i, err)
		}
		if err := out.WriteString(text); err != nil {
			return err
		}
		if err := out.WriteString("\n"); err != nil {
			return err
		}
	}
	return nil
}
