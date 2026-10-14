package usecases

import (
	"context"
	"errors"
	"reflect"
	"testing"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"github.com/gsoultan/panmail/internal/email_provider/senders"
)

func newSendersTestUsecase() (ManageProvidersUsecase, *mockRepo) {
	repo := &mockRepo{providers: make(map[string]*entities.EmailProvider)}
	return NewManageProvidersUsecase(repo, NewProviderFactory()), repo
}

func sesCreate(name string, allowedSenders []string) *panmailv1.CreateEmailProviderRequest {
	return &panmailv1.CreateEmailProviderRequest{
		Name: name,
		Type: panmailv1.ProviderType_PROVIDER_TYPE_SES,
		Config: &panmailv1.CreateEmailProviderRequest_Ses{
			Ses: &panmailv1.SesConfig{Region: "us-east-1", AccessKey: "AKIA", SecretKey: "secret"},
		},
		AllowedSenders: allowedSenders,
	}
}

// Entries are stored the way they will be matched, so what the form shows
// after a save is what the gateway enforces.
func TestCreateStoresAllowedSendersInCanonicalForm(t *testing.T) {
	u, repo := newSendersTestUsecase()

	got, err := u.Create(context.Background(), testTenantID,
		sesCreate("SES", []string{"Billing@Acme.com", "acme.org", "billing@acme.com"}))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"billing@acme.com", "*@acme.org"}
	if !reflect.DeepEqual(got.AllowedSenders, want) {
		t.Errorf("response AllowedSenders = %v, want %v", got.AllowedSenders, want)
	}
	if stored := repo.providers[got.Id].AllowedSenders; !reflect.DeepEqual(stored, want) {
		t.Errorf("stored AllowedSenders = %v, want %v", stored, want)
	}
}

// An entry that cannot be matched is refused with its name, not stored to be
// ignored at delivery.
func TestCreateRefusesAnInvalidAllowedSender(t *testing.T) {
	u, repo := newSendersTestUsecase()

	_, err := u.Create(context.Background(), testTenantID,
		sesCreate("SES", []string{"ok@acme.com", "*.acme.com"}))

	var invalid *senders.InvalidEntryError
	if !errors.As(err, &invalid) || invalid.Entry != "*.acme.com" {
		t.Fatalf("error = %v, want an InvalidEntryError naming *.acme.com", err)
	}
	if len(repo.providers) != 0 {
		t.Errorf("a provider was stored despite the refusal")
	}
}

// The list is replaced on update, so an empty one lifts the restriction.
// Before the form sent it, every save from the UI did that by accident.
func TestUpdateReplacesAllowedSenders(t *testing.T) {
	u, repo := newSendersTestUsecase()
	created, err := u.Create(context.Background(), testTenantID,
		sesCreate("SES", []string{"*@acme.com"}))
	if err != nil {
		t.Fatal(err)
	}

	update := func(entries []string) *panmailv1.EmailProvider {
		t.Helper()
		got, err := u.Update(context.Background(), testTenantID, &panmailv1.UpdateEmailProviderRequest{
			Id:             created.Id,
			Name:           "SES",
			AllowedSenders: entries,
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	got := update([]string{"billing@acme.com"})
	if !reflect.DeepEqual(got.AllowedSenders, []string{"billing@acme.com"}) {
		t.Errorf("after update AllowedSenders = %v", got.AllowedSenders)
	}

	got = update(nil)
	if len(got.AllowedSenders) != 0 || len(repo.providers[created.Id].AllowedSenders) != 0 {
		t.Errorf("an empty list did not lift the restriction: %v", got.AllowedSenders)
	}
}
