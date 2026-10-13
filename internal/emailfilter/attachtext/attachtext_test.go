package attachtext_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gsoultan/panmail/internal/emailfilter/attachtext"
)

// zipOf builds an Office-style package from part name to content.
func zipOf(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const wordNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

func docx(t *testing.T, body, header string) []byte {
	parts := map[string]string{
		"[Content_Types].xml": `<Types/>`,
		"word/document.xml":   `<w:document ` + wordNS + `><w:body>` + body + `</w:body></w:document>`,
	}
	if header != "" {
		parts["word/header1.xml"] = `<w:hdr ` + wordNS + `>` + header + `</w:hdr>`
	}
	return zipOf(t, parts)
}

// pdfWith builds a one-page PDF showing text in Helvetica, with a correct
// cross-reference table, which is what a real reader insists on.
func pdfWith(text string) []byte {
	content := fmt.Sprintf("BT /F1 24 Tf 72 700 Td (%s) Tj ET", text)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return buf.Bytes()
}

func TestDetectByExtensionThenContentType(t *testing.T) {
	cases := []struct {
		name, ct string
		want     attachtext.Format
	}{
		{"Payroll.PDF", "", attachtext.FormatPDF},
		{"memo.docx", "", attachtext.FormatDOCX},
		{"budget.xlsm", "", attachtext.FormatXLSX},
		{"noext", "application/pdf; name=x", attachtext.FormatPDF},
		{"noext", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", attachtext.FormatXLSX},
		{"old.doc", "application/msword", attachtext.FormatNone},
		{"photo.jpg", "image/jpeg", attachtext.FormatNone},
	}
	for _, c := range cases {
		if got := attachtext.Detect(c.name, c.ct); got != c.want {
			t.Errorf("Detect(%q, %q) = %q, want %q", c.name, c.ct, got, c.want)
		}
	}
}

func TestWordTextIncludesBodyAndHeader(t *testing.T) {
	// A phrase split across runs must read as one phrase, and paragraphs must
	// not run together.
	data := docx(t,
		`<w:p><w:r><w:t>Gaji </w:t></w:r><w:r><w:t>karyawan</w:t></w:r></w:p><w:p><w:r><w:t>Bulan Oktober</w:t></w:r></w:p>`,
		`<w:p><w:r><w:t>RAHASIA</w:t></w:r></w:p>`)

	text, err := attachtext.Extract("memo.docx", "", data)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for _, want := range []string{"Gaji karyawan\n", "Bulan Oktober", "RAHASIA"} {
		if !strings.Contains(text, want) {
			t.Errorf("text %q does not contain %q", text, want)
		}
	}
}

func TestExcelTextIncludesSharedStringsAndNumbers(t *testing.T) {
	data := zipOf(t, map[string]string{
		"xl/sharedStrings.xml": `<sst><si><t>Nomor rekening</t></si><si><r><t>Bank </t></r><r><t>Mandiri</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData>` +
			`<row><c t="s"><v>0</v></c><c><v>1234567890</v></c></row>` +
			`<row><c t="inlineStr"><is><t>catatan internal</t></is></c></row>` +
			`</sheetData></worksheet>`,
	})

	text, err := attachtext.Extract("data.xlsx", "", data)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for _, want := range []string{"Nomor rekening", "Bank Mandiri", "1234567890", "catatan internal"} {
		if !strings.Contains(text, want) {
			t.Errorf("text %q does not contain %q", text, want)
		}
	}
	// A shared-string index is not text anyone typed.
	if strings.Contains(text, "0\t1234567890") {
		t.Errorf("text %q contains a shared-string index", text)
	}
}

func TestPDFText(t *testing.T) {
	text, err := attachtext.Extract("laporan.pdf", "application/pdf", pdfWith("Dokumen rahasia"))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !strings.Contains(text, "Dokumen rahasia") {
		t.Errorf("text %q does not contain the page text", text)
	}
}

func TestUnsupportedAndOversizedAreRefused(t *testing.T) {
	if _, err := attachtext.Extract("old.doc", "", []byte("x")); !errors.Is(err, attachtext.ErrUnsupported) {
		t.Errorf("old .doc: err = %v, want ErrUnsupported", err)
	}
	big := make([]byte, attachtext.MaxInputBytes+1)
	if _, err := attachtext.Extract("big.pdf", "", big); !errors.Is(err, attachtext.ErrTooLarge) {
		t.Errorf("oversized: err = %v, want ErrTooLarge", err)
	}
}

func TestMalformedDocumentsFailWithoutPanicking(t *testing.T) {
	garbage := []byte("%PDF-1.4\nthis is not a pdf at all\n%%EOF")
	for _, name := range []string{"x.pdf", "x.docx", "x.xlsx"} {
		if _, err := attachtext.Extract(name, "", garbage); err == nil {
			t.Errorf("%s: expected an error for garbage input", name)
		}
	}
	// A zip that is not an Office document.
	if _, err := attachtext.Extract("x.docx", "", zipOf(t, map[string]string{"readme.txt": "hi"})); err == nil {
		t.Error("expected an error for a zip with no Word parts")
	}
}

func TestTextIsCappedWithoutSplittingCharacters(t *testing.T) {
	// "é" is two bytes; enough of them to pass the cap at an odd offset.
	word := strings.Repeat("é", attachtext.MaxTextBytes)
	data := docx(t, `<w:p><w:r><w:t>a`+word+`</w:t></w:r></w:p>`, "")

	text, err := attachtext.Extract("long.docx", "", data)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(text) > attachtext.MaxTextBytes {
		t.Errorf("len(text) = %d, over the cap %d", len(text), attachtext.MaxTextBytes)
	}
	if !strings.HasSuffix(text, "é") {
		t.Errorf("text ends mid-character: %q", text[len(text)-4:])
	}
}

func TestADecompressionBombStopsAtTheBudget(t *testing.T) {
	// Compresses to a few hundred kilobytes, expands far past the budget, and
	// holds no text, so only the budget can stop it.
	filler := strings.Repeat(" ", 100<<20)
	data := zipOf(t, map[string]string{
		"word/document.xml": `<w:document ` + wordNS + `>` + filler + `</w:document>`,
	})
	if len(data) > attachtext.MaxInputBytes {
		t.Fatalf("test bomb is %d bytes, too big to be read at all", len(data))
	}
	if _, err := attachtext.Extract("bomb.docx", "", data); err == nil {
		t.Fatal("expected the decompression budget to stop the read")
	}
}
