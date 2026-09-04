package usecases

import (
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"google.golang.org/protobuf/proto"
)

// The settings page has to show what is being enforced. A deployment that has
// never opened this setting is redacting passwords, so that is what it shows —
// not UNSPECIFIED, which is not a behaviour.
func TestAnUnsetLevelReadsAsThePasswordDefault(t *testing.T) {
	usecase, _ := newUsecase(nil)

	got, err := usecase.GetSettings(t.Context())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.ContentRedaction != panmailv1.ContentRedaction_CONTENT_REDACTION_PASSWORDS {
		t.Errorf("content redaction = %v, want PASSWORDS on a first run", got.ContentRedaction)
	}
}

func TestTheLevelRoundTrips(t *testing.T) {
	for _, want := range []panmailv1.ContentRedaction{
		panmailv1.ContentRedaction_CONTENT_REDACTION_OFF,
		panmailv1.ContentRedaction_CONTENT_REDACTION_PASSWORDS,
		panmailv1.ContentRedaction_CONTENT_REDACTION_CODES,
		panmailv1.ContentRedaction_CONTENT_REDACTION_SECRETS,
	} {
		usecase, _ := newUsecase(nil)
		if _, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
			ContentRedaction: want,
		}); err != nil {
			t.Fatalf("UpdateSettings(%v): %v", want, err)
		}
		got, err := usecase.GetSettings(t.Context())
		if err != nil {
			t.Fatalf("GetSettings: %v", err)
		}
		if got.ContentRedaction != want {
			t.Errorf("stored %v, read back %v", want, got.ContentRedaction)
		}
	}
}

// The one field here that is not full-replace, deliberately.
//
// Every retention policy is reset by a request that omits it — see the note in
// the retention memory. Redaction must not behave that way: a caller updating
// base_url would otherwise turn off password masking by omission, which is a
// disclosure introduced by a request that says nothing about disclosure.
func TestAnOmittedLevelIsLeftAloneRatherThanReset(t *testing.T) {
	usecase, _ := newUsecase(nil)

	if _, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		ContentRedaction: panmailv1.ContentRedaction_CONTENT_REDACTION_SECRETS,
	}); err != nil {
		t.Fatalf("first update: %v", err)
	}

	// A later caller changing something unrelated, with no redaction field.
	if _, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		BaseUrl: proto.String("https://mail.example.com"),
	}); err != nil {
		t.Fatalf("second update: %v", err)
	}

	got, err := usecase.GetSettings(t.Context())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.ContentRedaction != panmailv1.ContentRedaction_CONTENT_REDACTION_SECRETS {
		t.Errorf("content redaction = %v after an unrelated update, want SECRETS to survive",
			got.ContentRedaction)
	}
	if got.GetBaseUrl() != "https://mail.example.com" {
		t.Errorf("base url = %q, want the update to have applied", got.GetBaseUrl())
	}
}

// Turning it off has to be expressible. If OFF were indistinguishable from
// "not set", an operator could never actually disable it.
func TestOffIsDistinctFromUnset(t *testing.T) {
	usecase, _ := newUsecase(nil)

	if _, err := usecase.UpdateSettings(t.Context(), &panmailv1.SystemSettings{
		ContentRedaction: panmailv1.ContentRedaction_CONTENT_REDACTION_OFF,
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	got, err := usecase.GetSettings(t.Context())
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.ContentRedaction != panmailv1.ContentRedaction_CONTENT_REDACTION_OFF {
		t.Errorf("content redaction = %v, want OFF to stick", got.ContentRedaction)
	}
}
