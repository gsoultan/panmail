package http

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/inbound/repositories/stores"
	inboundpostgres "github.com/gsoultan/panmail/internal/inbound/repositories/stores/postgres"
	"github.com/gsoultan/panmail/internal/inbound/usecases"
	"github.com/gsoultan/panmail/internal/storetest"
)

// A provider retries any post it did not see a 2xx for, and a slow answer
// reads to it exactly like a failure. Every retry used to be a new message:
// stored, screened, bounce-checked and announced to the tenant's webhooks
// again.

func dedupingHandler(t *testing.T, uc usecases.InboundUsecase) (*WebhookHandler, stores.ReceiptRepository) {
	t.Helper()
	receipts := inboundpostgres.NewReceiptStore(storetest.NewConnection(t))
	return NewWebhookHandler(uc, defaultProviders()).WithReceipts(receipts), receipts
}

func TestARetriedPostIsStoredOnce(t *testing.T) {
	cases := map[string]string{
		"by the poster's event id": `{"event_id":"evt-1","from":"a@b.c","to":["d@e.f"],"subject":"hi","text":"x"}`,
		"by Message-ID and content": `{"from":"a@b.c","to":["d@e.f"],"subject":"hi","text":"x",` +
			`"headers":{"Message-ID":"<m-1@b.c>"}}`,
		"by a message_id field": `{"message_id":"<m-1@b.c>","from":"a@b.c","to":["d@e.f"],"subject":"hi","text":"x"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			uc := &stubUsecase{}
			h, _ := dedupingHandler(t, uc)

			for attempt := 1; attempt <= 3; attempt++ {
				if rec := signedJSON(t, h, body); rec.Code != http.StatusAccepted {
					t.Fatalf("attempt %d: status %d, want 202 (a duplicate answers success)", attempt, rec.Code)
				}
			}
			if len(uc.received) != 1 {
				t.Fatalf("processed %d times, want once", len(uc.received))
			}
		})
	}
}

// A multipart retry is built afresh, with a new boundary, so its bytes differ
// from the first post's even though the message is the same. The key is taken
// from the parsed message, not from the body.
func TestAMultipartRetryWithANewBoundaryIsStillADuplicate(t *testing.T) {
	uc := &stubUsecase{}
	h, _ := dedupingHandler(t, uc)

	for attempt := 1; attempt <= 2; attempt++ {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf) // a random boundary each time
		_ = mw.WriteField("from", "a@b.c")
		_ = mw.WriteField("to", "d@e.f")
		_ = mw.WriteField("subject", "hi")
		_ = mw.WriteField("text", "x")
		_ = mw.WriteField("headers", "Message-ID: <m-1@b.c>\r\nSubject: hi")
		_ = mw.Close()

		req := httptest.NewRequest(http.MethodPost, inboundPath(tenantA, providerA), bytes.NewReader(buf.Bytes()))
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set(SignatureHeader, sign(secretA, buf.Bytes()))
		if rec := serve(h, req); rec.Code != http.StatusAccepted {
			t.Fatalf("attempt %d: status %d, want 202", attempt, rec.Code)
		}
	}
	if len(uc.received) != 1 {
		t.Fatalf("processed %d times, want once", len(uc.received))
	}
}

// What is not the same message is not deduplicated: a Message-ID is chosen by
// whoever sent the mail, and on its own cannot tell two messages apart, and a
// post with no identifier at all cannot be recognised when it comes again.
func TestDifferentMessagesAreNotTakenForDuplicates(t *testing.T) {
	cases := map[string][2]string{
		"same Message-ID, different content": {
			`{"from":"a@b.c","to":["d@e.f"],"subject":"one","headers":{"Message-ID":"<m@b.c>"}}`,
			`{"from":"a@b.c","to":["d@e.f"],"subject":"two","headers":{"Message-ID":"<m@b.c>"}}`,
		},
		"different event ids": {
			`{"event_id":"e1","from":"a@b.c","subject":"same"}`,
			`{"event_id":"e2","from":"a@b.c","subject":"same"}`,
		},
		"no identifier at all": {
			`{"from":"a@b.c","to":["d@e.f"],"subject":"same"}`,
			`{"from":"a@b.c","to":["d@e.f"],"subject":"same"}`,
		},
	}
	for name, bodies := range cases {
		t.Run(name, func(t *testing.T) {
			uc := &stubUsecase{}
			h, _ := dedupingHandler(t, uc)
			for _, body := range bodies {
				if rec := signedJSON(t, h, body); rec.Code != http.StatusAccepted {
					t.Fatalf("status %d, want 202", rec.Code)
				}
			}
			if len(uc.received) != 2 {
				t.Fatalf("processed %d, want both", len(uc.received))
			}
			if name != "no identifier at all" && uc.received[0].Id == uc.received[1].Id {
				t.Fatal("two different messages were given the same id")
			}
		})
	}
}

// A post that fails must not leave a receipt behind. Otherwise the provider's
// retry -- the one that would have delivered the message -- is answered as a
// duplicate, and the message is lost with a 202.
func TestAFailedPostDoesNotMakeItsRetryADuplicate(t *testing.T) {
	uc := &stubUsecase{err: errors.New("disk full")}
	h, _ := dedupingHandler(t, uc)
	body := `{"event_id":"evt-1","from":"a@b.c","subject":"hi"}`

	if rec := signedJSON(t, h, body); rec.Code != http.StatusInternalServerError {
		t.Fatalf("failing post: status %d, want 500", rec.Code)
	}
	uc.err = nil
	if rec := signedJSON(t, h, body); rec.Code != http.StatusAccepted {
		t.Fatalf("retry: status %d, want 202", rec.Code)
	}
	if len(uc.received) != 2 {
		t.Fatalf("processed %d times, want the retry processed too", len(uc.received))
	}
}

// While another post of the message is still being processed, a retry is told
// to come back rather than told it succeeded: the first may yet fail, and a
// provider that saw a 2xx would never send the message again.
func TestARetryWhileTheFirstIsInFlightIsAskedToComeBack(t *testing.T) {
	uc := &stubUsecase{}
	h, receipts := dedupingHandler(t, uc)
	body := `{"event_id":"evt-1","from":"a@b.c","subject":"hi"}`

	// The first post holds the claim and has not finished.
	got, _, err := receipts.Claim(context.Background(), tenantA, providerA,
		dedupeKey(&inboundPayload{EventID: "evt-1"}), time.Minute)
	if err != nil || got != stores.ClaimAcquired {
		t.Fatalf("seeding the in-flight claim = %v, %v", got, err)
	}

	rec := signedJSON(t, h, body)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After on the in-flight answer")
	}
	if len(uc.received) != 0 {
		t.Fatal("processed a message another post holds the claim for")
	}
}

// slowUsecase holds each message for a moment, so concurrent posts overlap,
// and counts under a lock.
type slowUsecase struct {
	stubUsecase
	mu        sync.Mutex
	processed int
}

func (s *slowUsecase) Process(context.Context, *panmailv1.InboundEmail, *usecases.DeliveryReport) error {
	time.Sleep(20 * time.Millisecond)
	s.mu.Lock()
	s.processed++
	s.mu.Unlock()
	return nil
}

// Concurrent posts of the same message -- a provider retrying a post that is
// still in flight, onto another connection -- must not both be processed. A
// check-then-write lets every one of them past the check.
func TestConcurrentDuplicatePostsAreProcessedOnce(t *testing.T) {
	uc := &slowUsecase{}
	h, _ := dedupingHandler(t, uc)
	body := `{"event_id":"evt-1","from":"a@b.c","subject":"hi"}`

	const posts = 12
	codes := make([]int, posts)
	var wg sync.WaitGroup
	for i := range posts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = signedJSON(t, h, body).Code
		}()
	}
	wg.Wait()

	if uc.processed != 1 {
		t.Fatalf("processed %d times, want once", uc.processed)
	}
	for i, code := range codes {
		if code != http.StatusAccepted && code != http.StatusServiceUnavailable {
			t.Errorf("post %d: status %d, want 202 or 503", i, code)
		}
	}
}

type failingReceipts struct{ stores.ReceiptRepository }

func (failingReceipts) Claim(context.Context, string, string, string, time.Duration) (stores.ClaimResult, string, error) {
	return 0, "", errors.New("database is down")
}

// A receipt store that cannot answer must not cost the message: refusing it
// would lose it if the provider gave up.
func TestAnUnreachableReceiptStoreDoesNotRefuseMail(t *testing.T) {
	uc := &stubUsecase{}
	h := NewWebhookHandler(uc, defaultProviders()).WithReceipts(failingReceipts{})

	if rec := signedJSON(t, h, `{"event_id":"evt-1","from":"a@b.c","subject":"hi"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", rec.Code)
	}
	if len(uc.received) != 1 {
		t.Fatal("the message was not processed")
	}
}

// The stored id is derived from the key, so a retry that gets past the
// receipts -- because they could not be reached -- still lands on the id the
// inbound store already holds, and the usecase skips it there.
func TestTheStoredIDIsDerivedFromTheMessage(t *testing.T) {
	uc := &stubUsecase{}
	h := NewWebhookHandler(uc, defaultProviders())
	body := `{"event_id":"evt-1","from":"a@b.c","subject":"hi"}`

	signedJSON(t, h, body)
	signedJSON(t, h, body)
	if len(uc.received) != 2 || uc.received[0].Id != uc.received[1].Id {
		t.Fatalf("ids %q, want the same id for the same message", ids(uc.received))
	}
}

func ids(es []*panmailv1.InboundEmail) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Id
	}
	return out
}
