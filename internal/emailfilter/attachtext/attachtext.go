// Package attachtext pulls the readable text out of the attachment formats a
// content rule is most often written about: PDF, Word and Excel.
//
// It exists so a rule like "hold anything that mentions the payroll" catches
// the payroll attached as a spreadsheet, not only the payroll pasted into the
// body. Everything here is bounded: an attachment is input from whoever sent
// the message, and a document that decompresses to gigabytes, or a PDF with a
// million pages, must cost a bounded amount on the send path rather than
// whatever its author chose.
//
// Only the formats named are read. The older binary .doc and .xls, images and
// scanned PDFs have no text this package can reach, and are reported as
// unsupported or empty rather than guessed at.
package attachtext

import (
	"errors"
	"path/filepath"
	"strings"
)

// Limits. Exported so callers and tests can reason about them.
const (
	// MaxInputBytes is the largest attachment read at all. Larger ones are
	// skipped: the size rules already exist for "refuse anything this big".
	MaxInputBytes = 25 << 20

	// MaxTextBytes is how much text is kept from one attachment. A rule is
	// looking for a word or a pattern, and the first megabyte of a document
	// is where a person would put what a rule is about.
	MaxTextBytes = 1 << 20

	// maxUnzippedBytes bounds what a Word or Excel file may decompress to,
	// across every part read. It is what stops a zip bomb.
	maxUnzippedBytes = 64 << 20

	// maxPDFPages bounds the pages read from one PDF.
	maxPDFPages = 1000
)

var (
	// ErrUnsupported is a format this package does not read.
	ErrUnsupported = errors.New("attachtext: unsupported attachment format")

	// ErrTooLarge is an attachment over MaxInputBytes.
	ErrTooLarge = errors.New("attachtext: attachment is too large to read")
)

// Format is a document type this package can read.
type Format string

const (
	FormatNone Format = ""
	FormatPDF  Format = "pdf"
	FormatDOCX Format = "docx"
	FormatXLSX Format = "xlsx"
)

// Detect decides the format from the file name, falling back to the declared
// content type for a file sent without an extension.
func Detect(filename, contentType string) Format {
	switch strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".") {
	case "pdf":
		return FormatPDF
	case "docx", "docm", "dotx", "dotm":
		return FormatDOCX
	case "xlsx", "xlsm", "xltx", "xltm":
		return FormatXLSX
	}

	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch {
	case ct == "application/pdf":
		return FormatPDF
	case strings.HasPrefix(ct, "application/vnd.openxmlformats-officedocument.wordprocessingml"),
		strings.HasPrefix(ct, "application/vnd.ms-word.document.macroenabled"):
		return FormatDOCX
	case strings.HasPrefix(ct, "application/vnd.openxmlformats-officedocument.spreadsheetml"),
		strings.HasPrefix(ct, "application/vnd.ms-excel.sheet.macroenabled"):
		return FormatXLSX
	}
	return FormatNone
}

// Supported reports whether Extract can read the attachment.
func Supported(filename, contentType string) bool {
	return Detect(filename, contentType) != FormatNone
}

// Extract returns the text of a PDF, Word or Excel attachment, at most
// MaxTextBytes of it.
//
// A document that cannot be read returns an error and whatever text was read
// before the failure. A panic in a parser is recovered and returned as an
// error: a malformed attachment is something a sender controls, and it must
// not be able to take the gateway down.
func Extract(filename, contentType string, data []byte) (text string, err error) {
	format := Detect(filename, contentType)
	if format == FormatNone {
		return "", ErrUnsupported
	}
	if len(data) > MaxInputBytes {
		return "", ErrTooLarge
	}

	out := newLimitedText(MaxTextBytes)
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("attachtext: the document could not be parsed")
		}
		text = out.String()
	}()

	switch format {
	case FormatPDF:
		err = extractPDF(data, out)
	case FormatDOCX:
		err = extractDOCX(data, out)
	case FormatXLSX:
		err = extractXLSX(data, out)
	}
	if errors.Is(err, errTextFull) {
		err = nil
	}
	return out.String(), err
}

// errTextFull stops a parser once MaxTextBytes has been collected. It is not a
// failure: the text kept is the text a rule will see.
var errTextFull = errors.New("attachtext: text limit reached")

// limitedText collects text up to a byte limit, then refuses more.
type limitedText struct {
	b     strings.Builder
	limit int
}

func newLimitedText(limit int) *limitedText {
	return &limitedText{limit: limit}
}

// WriteString keeps what fits and reports errTextFull once nothing more does.
func (t *limitedText) WriteString(s string) error {
	room := t.limit - t.b.Len()
	if room <= 0 {
		return errTextFull
	}
	if len(s) > room {
		t.b.WriteString(truncateUTF8(s, room))
		return errTextFull
	}
	t.b.WriteString(s)
	return nil
}

func (t *limitedText) String() string {
	return t.b.String()
}

// truncateUTF8 cuts s to at most n bytes without splitting a character.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
