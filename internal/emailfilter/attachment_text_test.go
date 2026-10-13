package emailfilter_test

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"

	"github.com/gsoultan/panmail/internal/emailfilter"
)

// staticRules serves a fixed rule set; only Enabled is used by Screen.
type staticRules struct {
	emailfilter.RuleRepository
	rules []emailfilter.Rule
}

func (s staticRules) Enabled(context.Context, string, emailfilter.Direction) ([]emailfilter.Rule, error) {
	return s.rules, nil
}

func wordFile(t *testing.T, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` +
		text + `</w:t></w:r></w:p></w:body></w:document>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestScreenReadsTheTextInsideAnAttachment(t *testing.T) {
	hold := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold,
		cond(emailfilter.FieldAttachmentText, emailfilter.OpContains, "rahasia"))
	s := emailfilter.NewScreener(staticRules{rules: []emailfilter.Rule{hold}}, newFakeQuarantine(), 0, nil)

	withAttachment := func(text string) emailfilter.Message {
		data := wordFile(t, text)
		return msg(func(m *emailfilter.Message) {
			m.Attachments = []emailfilter.Attachment{{Filename: "memo.docx", Size: int64(len(data)), Data: data}}
		})
	}

	d, err := s.Screen(context.Background(), "tenant", emailfilter.DirectionOutbound, withAttachment("Dokumen RAHASIA perusahaan"))
	if err != nil {
		t.Fatalf("Screen: %v", err)
	}
	if d.Action != emailfilter.ActionHold {
		t.Errorf("a Word file mentioning the keyword: action = %q, want hold", d.Action)
	}

	d, err = s.Screen(context.Background(), "tenant", emailfilter.DirectionOutbound, withAttachment("Undangan rapat"))
	if err != nil {
		t.Fatalf("Screen: %v", err)
	}
	if d.Action != "" {
		t.Errorf("a Word file without the keyword: action = %q, want none", d.Action)
	}
}

func TestAttachmentTextConditionIgnoresTheFileName(t *testing.T) {
	// The field is about what is inside. A file called rahasia.docx with
	// nothing sensitive in it is a name rule's business, not this one's.
	r := rule(t, emailfilter.DirectionOutbound, emailfilter.ActionHold,
		cond(emailfilter.FieldAttachmentText, emailfilter.OpContains, "rahasia"))
	m := msg(func(m *emailfilter.Message) {
		m.Attachments = []emailfilter.Attachment{{Filename: "rahasia.docx", Text: "Undangan rapat"}}
	})
	if d := emailfilter.Evaluate([]emailfilter.Rule{r}, emailfilter.DirectionOutbound, m); d.Action != "" {
		t.Errorf("action = %q, want none", d.Action)
	}
}
