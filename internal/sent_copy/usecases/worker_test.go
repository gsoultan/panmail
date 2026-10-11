package usecases

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend"
	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/server"
	"github.com/gsoultan/gsmail"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	providerEntities "github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	providerStores "github.com/gsoultan/panmail/internal/email_provider/repositories/stores"
	"github.com/gsoultan/panmail/internal/sent_copy/entities"
	"github.com/gsoultan/panmail/internal/sent_copy/repositories"
	"github.com/gsoultan/panmail/internal/sent_copy/repositories/stores/postgres"
	"github.com/gsoultan/panmail/internal/storetest"
	"github.com/gsoultan/panmail/pkg/secrets"
)

// --- a real IMAP server -----------------------------------------------------

// loginBackend accepts a fixed set of logins and maps them all onto the
// memory backend's single user, so a test can assert which login was used.
type loginBackend struct {
	mem    *memory.Backend
	logins map[string]string
}

func (b *loginBackend) Login(info *imap.ConnInfo, username, password string) (backend.User, error) {
	if want, ok := b.logins[username]; ok && want == password {
		return b.mem.Login(info, "username", "password")
	}
	return nil, errors.New("bad credentials")
}

type imapServer struct {
	set   *entities.Settings
	roots *x509.CertPool
	user  backend.User
}

// startIMAP runs an IMAP server on loopback. implicitTLS serves TLS from the
// first byte; otherwise STARTTLS is offered only when offerSTARTTLS is set.
func startIMAP(t *testing.T, logins map[string]string, implicitTLS, offerSTARTTLS bool) *imapServer {
	t.Helper()
	cert, roots := selfSigned(t)
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}

	mem := memory.New()
	user, err := mem.Login(nil, "username", "password")
	if err != nil {
		t.Fatal(err)
	}
	if err := user.CreateMailbox("Sent"); err != nil {
		t.Fatal(err)
	}

	srv := server.New(&loginBackend{mem: mem, logins: logins})
	if offerSTARTTLS {
		srv.TLSConfig = tlsConfig
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if implicitTLS {
		l = tls.NewListener(l, tlsConfig)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })

	port := l.Addr().(*net.TCPAddr).Port
	return &imapServer{
		set: &entities.Settings{
			IMAPHost: "127.0.0.1", IMAPPort: port, IMAPUseSSL: implicitTLS, SentFolder: "Sent",
		},
		roots: roots,
		user:  user,
	}
}

func (s *imapServer) sentMessages(t *testing.T) []string {
	t.Helper()
	mbox, err := s.user.GetMailbox("Sent")
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan *imap.Message, 10)
	section := &imap.BodySectionName{}
	seq, _ := imap.ParseSeqSet("1:*")
	if err := mbox.ListMessages(false, seq, []imap.FetchItem{section.FetchItem()}, ch); err != nil {
		t.Fatal(err)
	}
	var out []string
	for m := range ch {
		body, _ := io.ReadAll(m.GetBody(section))
		out = append(out, string(body))
	}
	return out
}

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "imap.test"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// --- fakes for Bcc to sender -------------------------------------------------

type fakeProviders struct {
	providerStores.Repository
	provider *providerEntities.EmailProvider
}

func (f *fakeProviders) GetByID(_ context.Context, tenantID, id string) (*providerEntities.EmailProvider, error) {
	if f.provider != nil && f.provider.ID == id && f.provider.TenantID == tenantID {
		return f.provider, nil
	}
	return nil, nil
}

type fakeFactory struct {
	providerEntities.ProviderFactory
	sender *fakeSender
}

func (f *fakeFactory) CreateSender(*providerEntities.EmailProvider) (gsmail.Sender, error) {
	return f.sender, nil
}

type fakeSender struct {
	gsmail.Sender
	sent []gsmail.Email
}

func (f *fakeSender) Send(_ context.Context, e gsmail.Email) error {
	f.sent = append(f.sent, e)
	return nil
}

// --- harness -----------------------------------------------------------------

const testProviderID = "33333333-3333-3333-3333-333333333333"

type harness struct {
	repo    repositories.Repository
	queue   *Queue
	worker  *Worker
	sender  *fakeSender
	now     time.Time
	appends *IMAPAppender
}

func newHarness(t *testing.T, providerType panmailv1.ProviderType) *harness {
	t.Helper()
	key, _ := secrets.GenerateKey()
	ring, _ := secrets.NewKeyring(key)
	repo := postgres.NewStore(storetest.NewConnection(t), ring)

	sender := &fakeSender{}
	appender := &IMAPAppender{}
	worker := NewWorker(repo, appender,
		&fakeProviders{provider: &providerEntities.EmailProvider{ID: testProviderID, TenantID: storetest.TenantA, Type: providerType}},
		&fakeFactory{sender: sender})
	h := &harness{repo: repo, worker: worker, sender: sender, now: time.Now(), appends: appender}
	worker.now = func() time.Time { return h.now }
	h.queue = NewQueue(repo, nil)
	h.queue.now = func() time.Time { return h.now }
	return h
}

func (h *harness) send(t *testing.T) {
	t.Helper()
	err := h.queue.Enqueue(context.Background(), storetest.TenantA, storetest.ID("msg"), &panmailv1.SendEmailRequest{
		ProviderId: testProviderID,
		From:       "Alice <Alice@Example.com>",
		To:         []string{"bob@example.net"},
		Cc:         []string{"carol@example.net"},
		Subject:    "Quarterly plan",
		BodyText:   "See attached.",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
}

func (h *harness) settings(t *testing.T, s *entities.Settings) {
	t.Helper()
	s.TenantID = storetest.TenantA
	s.UpdatedAt = time.Now()
	if err := h.repo.SaveSettings(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) queued(t *testing.T) int {
	t.Helper()
	jobs, err := h.repo.ClaimJobs(context.Background(), h.now.Add(1000*time.Hour), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(jobs)
}

// --- tests -------------------------------------------------------------------

func TestNoneQueuesNothing(t *testing.T) {
	h := newHarness(t, panmailv1.ProviderType_PROVIDER_TYPE_SMTP)
	h.send(t)
	if n := h.queued(t); n != 0 {
		t.Fatalf("%d copies queued for a tenant with no setting", n)
	}

	h.settings(t, &entities.Settings{Mode: entities.ModeNone})
	h.send(t)
	if n := h.queued(t); n != 0 {
		t.Fatalf("%d copies queued with mode none", n)
	}
}

func TestSharedLoginFilesTheCopyAsTheMasterUser(t *testing.T) {
	for _, tc := range []struct {
		name               string
		implicit, starttls bool
	}{
		{"implicit TLS", true, false},
		{"STARTTLS", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startIMAP(t, map[string]string{"alice@example.com*admin": "master-pw"}, tc.implicit, tc.starttls)
			h := newHarness(t, panmailv1.ProviderType_PROVIDER_TYPE_SMTP)
			h.appends.RootCAs = srv.roots

			set := *srv.set
			set.Mode, set.MasterUsername, set.MasterPassword = entities.ModeSharedLogin, "admin", "master-pw"
			h.settings(t, &set)

			h.send(t)
			if n := h.worker.RunOnce(context.Background()); n != 1 {
				t.Fatalf("RunOnce claimed %d, want 1", n)
			}

			msgs := srv.sentMessages(t)
			if len(msgs) != 1 {
				t.Fatalf("Sent holds %d messages, want 1", len(msgs))
			}
			for _, want := range []string{"Subject: Quarterly plan", "bob@example.net", "carol@example.net"} {
				if !strings.Contains(msgs[0], want) {
					t.Errorf("the filed copy lacks %q", want)
				}
			}
			parsed, err := gsmail.ParseRawEmail([]byte(msgs[0]))
			if err != nil {
				t.Fatalf("parse the filed copy: %v", err)
			}
			if !strings.Contains(string(parsed.Body), "See attached.") {
				t.Errorf("the filed copy's body = %q", parsed.Body)
			}
			if n := h.queued(t); n != 0 {
				t.Errorf("a filed copy is still queued")
			}
		})
	}
}

func TestPerUserLoginUsesThatMailboxesLogin(t *testing.T) {
	srv := startIMAP(t, map[string]string{"alice": "alice-pw"}, true, false)
	h := newHarness(t, panmailv1.ProviderType_PROVIDER_TYPE_SMTP)
	h.appends.RootCAs = srv.roots

	set := *srv.set
	set.Mode = entities.ModePerUserLogin
	h.settings(t, &set)
	if err := h.repo.SaveMailbox(context.Background(), &entities.Mailbox{
		TenantID: storetest.TenantA, Address: "alice@example.com", Username: "alice", Password: "alice-pw", UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	h.send(t)
	h.worker.RunOnce(context.Background())
	if msgs := srv.sentMessages(t); len(msgs) != 1 {
		t.Fatalf("Sent holds %d messages, want 1", len(msgs))
	}
}

// A sender with no stored login cannot get a copy however often it is tried,
// so the copy is dropped rather than retried for a day.
func TestPerUserLoginWithoutAMailboxGivesUp(t *testing.T) {
	srv := startIMAP(t, nil, true, false)
	h := newHarness(t, panmailv1.ProviderType_PROVIDER_TYPE_SMTP)
	h.appends.RootCAs = srv.roots
	set := *srv.set
	set.Mode = entities.ModePerUserLogin
	h.settings(t, &set)

	h.send(t)
	h.worker.RunOnce(context.Background())
	if n := h.queued(t); n != 0 {
		t.Fatalf("a copy with no login to file it with is still queued")
	}
}

// A wrong password may be fixed by the administrator, so it is retried later.
func TestALoginFailureIsRetriedLater(t *testing.T) {
	srv := startIMAP(t, map[string]string{"alice@example.com*admin": "right"}, true, false)
	h := newHarness(t, panmailv1.ProviderType_PROVIDER_TYPE_SMTP)
	h.appends.RootCAs = srv.roots
	set := *srv.set
	set.Mode, set.MasterUsername, set.MasterPassword = entities.ModeSharedLogin, "admin", "wrong"
	h.settings(t, &set)

	h.send(t)
	h.worker.RunOnce(context.Background())
	if n := h.worker.RunOnce(context.Background()); n != 0 {
		t.Fatalf("a failed copy was retried at once")
	}

	h.now = h.now.Add(2 * firstRetry)
	jobs, _ := h.repo.ClaimJobs(context.Background(), h.now, claimLease, 10)
	if len(jobs) != 1 || jobs[0].Attempts != 1 || !strings.Contains(jobs[0].LastError, "log in") {
		t.Fatalf("retry = %+v", jobs)
	}
	if strings.Contains(jobs[0].LastError, "wrong") {
		t.Error("the stored error contains the password")
	}
}

// The password must never cross the wire in the clear: a server that offers
// no STARTTLS gets no login at all.
func TestNoLoginWithoutTLS(t *testing.T) {
	srv := startIMAP(t, map[string]string{"alice@example.com*admin": "pw"}, false, false)
	appender := &IMAPAppender{RootCAs: srv.roots}
	set := *srv.set
	set.MasterUsername = "admin"
	err := appender.Append(context.Background(), &set, set.MasterLogin("alice@example.com"), "pw", []byte("Subject: x\r\n\r\nx"), time.Now())
	if !errors.Is(err, ErrNoSTARTTLS) {
		t.Fatalf("Append = %v, want ErrNoSTARTTLS", err)
	}
}

func TestBccToSenderMailsTheSenderOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		typ      panmailv1.ProviderType
		wantTo   []string
		envelope bool
	}{
		{"smtp keeps the headers and uses the envelope", panmailv1.ProviderType_PROVIDER_TYPE_SMTP, []string{"bob@example.net"}, true},
		{"api provider addresses the sender alone", panmailv1.ProviderType_PROVIDER_TYPE_SENDGRID, []string{"alice@example.com"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.typ)
			h.settings(t, &entities.Settings{Mode: entities.ModeBccSender})
			h.send(t)
			h.worker.RunOnce(context.Background())

			if len(h.sender.sent) != 1 {
				t.Fatalf("sent %d copies, want 1", len(h.sender.sent))
			}
			got := h.sender.sent[0]
			if strings.Join(got.To, ",") != strings.Join(tc.wantTo, ",") {
				t.Errorf("To = %v, want %v", got.To, tc.wantTo)
			}
			if tc.envelope && (len(got.Envelope) != 1 || got.Envelope[0] != "alice@example.com") {
				t.Errorf("Envelope = %v, want only the sender", got.Envelope)
			}
			if !tc.envelope && (len(got.Envelope) != 0 || len(got.Cc) != 0) {
				t.Errorf("an API copy would reach others: Envelope=%v Cc=%v", got.Envelope, got.Cc)
			}
		})
	}
}

// Switching to None stops copies already waiting.
func TestACopyQueuedBeforeSwitchingToNoneIsDropped(t *testing.T) {
	h := newHarness(t, panmailv1.ProviderType_PROVIDER_TYPE_SMTP)
	h.settings(t, &entities.Settings{Mode: entities.ModeBccSender})
	h.send(t)
	h.settings(t, &entities.Settings{Mode: entities.ModeNone})

	h.worker.RunOnce(context.Background())
	if len(h.sender.sent) != 0 || h.queued(t) != 0 {
		t.Fatalf("sent %d, still queued %d; want nothing", len(h.sender.sent), h.queued(t))
	}
}

func TestRetryDelayDoublesUpToTheCap(t *testing.T) {
	if retryDelay(1) != firstRetry || retryDelay(2) != 2*firstRetry {
		t.Errorf("retryDelay(1,2) = %v, %v", retryDelay(1), retryDelay(2))
	}
	if retryDelay(maxAttempts) > maxRetry {
		t.Errorf("retryDelay exceeds the cap")
	}
}
