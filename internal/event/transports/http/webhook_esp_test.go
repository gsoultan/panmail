package http

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/gsmail"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
)

func newTestHandler() (*WebhookHandler, *recordingUsecase) {
	rec := &recordingUsecase{}
	return &WebhookHandler{
		processEventUsecase: rec,
		snsVerifier:         &gsmail.SNSVerifier{},
	}, rec
}

func basicAuth(user, pass string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	return h
}

func TestVerifyPostmark(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		header  http.Header
		wantErr bool
	}{
		{"user:pass matches", "hook:s3cret", basicAuth("hook", "s3cret"), false},
		{"password-only secret", "s3cret", basicAuth("", "s3cret"), false},
		{"wrong password", "hook:s3cret", basicAuth("hook", "nope"), true},
		{"wrong user", "hook:s3cret", basicAuth("other", "s3cret"), true},
		{"no authorization header", "hook:s3cret", http.Header{}, true},
		{"empty secret", "", basicAuth("hook", "s3cret"), true},
		{"not basic auth", "hook:s3cret", func() http.Header {
			h := http.Header{}
			h.Set("Authorization", "Bearer token")
			return h
		}(), true},
		{"undecodable base64", "hook:s3cret", func() http.Header {
			h := http.Header{}
			h.Set("Authorization", "Basic !!!!")
			return h
		}(), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyPostmark(tt.secret, tt.header)
			if (err != nil) != tt.wantErr {
				t.Fatalf("verifyPostmark() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTopicAllowed(t *testing.T) {
	const arn = "arn:aws:sns:us-east-1:123456789012:ses-events"

	tests := []struct {
		name       string
		configured string
		arn        string
		want       bool
	}{
		{"exact match", arn, arn, true},
		{"one of several", "arn:aws:sns:eu-west-1:1:other," + arn, arn, true},
		{"whitespace around entries", " " + arn + " , x", arn, true},
		{"different topic", "arn:aws:sns:us-east-1:123456789012:other", arn, false},
		{"empty configuration", "", arn, false},
		{"empty arn on the message", arn, "", false},
		{"prefix is not a match", arn, arn + ":extra", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := topicAllowed(tt.configured, tt.arn); got != tt.want {
				t.Fatalf("topicAllowed(%q, %q) = %v, want %v", tt.configured, tt.arn, got, tt.want)
			}
		})
	}
}

// Confirming a subscription is an outbound GET to a URL taken from the request
// body, so the host check is the whole defence. The lookalike cases are the
// point: a suffix match or a naive strings.Contains would pass all three.
func TestSNSSubscribeURLAllowed(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"genuine endpoint", "https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription", true},
		{"china partition", "https://sns.cn-north-1.amazonaws.com.cn/?Action=ConfirmSubscription", true},
		{"http is refused", "http://sns.us-east-1.amazonaws.com/", false},
		{"attacker domain with the host as a prefix", "https://sns.us-east-1.amazonaws.com.evil.test/", false},
		{"attacker domain with the host in the path", "https://evil.test/sns.us-east-1.amazonaws.com", false},
		{"attacker subdomain", "https://sns.us-east-1.amazonaws.com.attacker.test/", false},
		{"credentials pointing elsewhere", "https://sns.us-east-1.amazonaws.com@evil.test/", false},
		{"not a url", "://nonsense", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := snsSubscribeURLAllowed(tt.url); got != tt.want {
				t.Fatalf("snsSubscribeURLAllowed(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestPostmarkEvent(t *testing.T) {
	tests := []struct {
		name        string
		recordType  string
		bounceType  string
		description string
		details     string
		suppress    bool
		want        panmailv1.EmailEventType
		wantErrMsg  string
	}{
		{
			name: "hard bounce", recordType: "Bounce", bounceType: "HardBounce",
			description: "The server was unable to deliver", details: "550 no such user",
			want: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE, wantErrMsg: "550 no such user",
		},
		{
			name: "soft bounce", recordType: "Bounce", bounceType: "SoftBounce",
			description: "Mailbox full",
			want:        panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE, wantErrMsg: "Mailbox full",
		},
		{
			name: "an unfamiliar bounce type is soft, not hard", recordType: "Bounce", bounceType: "Transient",
			description: "Throttled",
			want:        panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE, wantErrMsg: "Throttled",
		},
		{
			name: "spam complaint", recordType: "SpamComplaint", description: "abuse",
			want: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT, wantErrMsg: "abuse",
		},
		{name: "delivery", recordType: "Delivery", want: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED},
		{name: "open", recordType: "Open", want: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED},
		{name: "click", recordType: "Click", want: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_CLICKED},
		{
			name: "subscription change that suppresses", recordType: "SubscriptionChange", suppress: true,
			want: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSUBSCRIBED,
		},
		{
			name: "reactivation is not an unsubscribe", recordType: "SubscriptionChange", suppress: false,
			want: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED,
		},
		{name: "unknown record type", recordType: "Nonsense", want: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errMsg := postmarkEvent(tt.recordType, tt.bounceType, tt.description, tt.details, tt.suppress)
			if got != tt.want {
				t.Fatalf("postmarkEvent() = %v, want %v", got, tt.want)
			}
			if errMsg != tt.wantErrMsg {
				t.Fatalf("postmarkEvent() errMsg = %q, want %q", errMsg, tt.wantErrMsg)
			}
		})
	}
}

func TestHandlePostmarkRecordsTheEvent(t *testing.T) {
	h, rec := newTestHandler()
	body := `{"RecordType":"Bounce","Type":"HardBounce","MessageID":"m-1",
	          "Email":"dead@example.com","Details":"550 no such user"}`

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/webhooks/t/p/postmark", strings.NewReader(body))
	h.handlePostmark(w, r, "t", "p", []byte(body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(rec.events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(rec.events))
	}
	got := rec.events[0]
	if got.eventType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE {
		t.Errorf("eventType = %v, want HARD_BOUNCE", got.eventType)
	}
	if got.recipient != "dead@example.com" || got.messageID != "m-1" {
		t.Errorf("got %+v", got)
	}
}

// Postmark names the address in Recipient on some record types and Email on
// others. Reading only one of them silently loses the recipient.
func TestHandlePostmarkFallsBackToRecipient(t *testing.T) {
	h, rec := newTestHandler()
	body := `{"RecordType":"Delivery","MessageID":"m-2","Recipient":"ok@example.com"}`

	r := httptest.NewRequest(http.MethodPost, "/webhooks/t/p/postmark", strings.NewReader(body))
	h.handlePostmark(httptest.NewRecorder(), r, "t", "p", []byte(body))

	if len(rec.events) != 1 || rec.events[0].recipient != "ok@example.com" {
		t.Fatalf("got %+v, want one event for ok@example.com", rec.events)
	}
}

func TestRecordSESNotification(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    []recordedEvent
	}{
		{
			name: "permanent bounce is a hard bounce",
			payload: `{"notificationType":"Bounce","mail":{"messageId":"m-1"},
			           "bounce":{"bounceType":"Permanent","bouncedRecipients":
			           [{"emailAddress":"dead@example.com","diagnosticCode":"550 unknown"}]}}`,
			want: []recordedEvent{{messageID: "m-1", eventType: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE, recipient: "dead@example.com", errorMessage: "550 unknown"}},
		},
		{
			name: "transient bounce is a soft bounce",
			payload: `{"notificationType":"Bounce","mail":{"messageId":"m-2"},
			           "bounce":{"bounceType":"Transient","bouncedRecipients":
			           [{"emailAddress":"full@example.com","status":"4.2.2"}]}}`,
			want: []recordedEvent{{messageID: "m-2", eventType: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SOFT_BOUNCE, recipient: "full@example.com", errorMessage: "4.2.2"}},
		},
		{
			name: "every bounced recipient is recorded",
			payload: `{"notificationType":"Bounce","mail":{"messageId":"m-3"},
			           "bounce":{"bounceType":"Permanent","bouncedRecipients":
			           [{"emailAddress":"a@example.com"},{"emailAddress":"b@example.com"}]}}`,
			want: []recordedEvent{
				{messageID: "m-3", eventType: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE, recipient: "a@example.com", errorMessage: ""},
				{messageID: "m-3", eventType: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE, recipient: "b@example.com", errorMessage: ""},
			},
		},
		{
			name: "complaint",
			payload: `{"notificationType":"Complaint","mail":{"messageId":"m-4"},
			           "complaint":{"complainedRecipients":[{"emailAddress":"cross@example.com"}],
			           "complaintFeedbackType":"abuse"}}`,
			want: []recordedEvent{{messageID: "m-4", eventType: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_SPAM_REPORT, recipient: "cross@example.com", errorMessage: "abuse"}},
		},
		{
			name: "delivery names its own recipients",
			payload: `{"notificationType":"Delivery","mail":{"messageId":"m-5","destination":["ignored@example.com"]},
			           "delivery":{"recipients":["real@example.com"]}}`,
			want: []recordedEvent{{messageID: "m-5", eventType: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED, recipient: "real@example.com", errorMessage: ""}},
		},
		{
			name:    "delivery falls back to the envelope destination",
			payload: `{"notificationType":"Delivery","mail":{"messageId":"m-6","destination":["to@example.com"]}}`,
			want:    []recordedEvent{{messageID: "m-6", eventType: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DELIVERED, recipient: "to@example.com", errorMessage: ""}},
		},
		{
			// Event Publishing uses eventType where feedback notifications use
			// notificationType. Reading only one understands half the tenants.
			name:    "event publishing shape is understood",
			payload: `{"eventType":"Open","mail":{"messageId":"m-7","destination":["reader@example.com"]}}`,
			want:    []recordedEvent{{messageID: "m-7", eventType: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_OPENED, recipient: "reader@example.com", errorMessage: ""}},
		},
		{
			name:    "reject carries its reason",
			payload: `{"eventType":"Reject","mail":{"messageId":"m-8","destination":["x@example.com"]},"reject":{"reason":"Bad content"}}`,
			want:    []recordedEvent{{messageID: "m-8", eventType: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_REJECTED, recipient: "x@example.com", errorMessage: "Bad content"}},
		},
		{
			name:    "delivery delay is deferred, not bounced",
			payload: `{"eventType":"DeliveryDelay","mail":{"messageId":"m-9","destination":["slow@example.com"]},"deliveryDelay":{"delayType":"MailboxFull"}}`,
			want:    []recordedEvent{{messageID: "m-9", eventType: panmailv1.EmailEventType_EMAIL_EVENT_TYPE_DEFERRED, recipient: "slow@example.com", errorMessage: "MailboxFull"}},
		},
		{
			name:    "an unknown type records nothing",
			payload: `{"notificationType":"RenderingFailure","mail":{"messageId":"m-10"}}`,
			want:    nil,
		},
		{
			name:    "a bounce with no bounce block records nothing",
			payload: `{"notificationType":"Bounce","mail":{"messageId":"m-11"}}`,
			want:    nil,
		},
		{
			name:    "unparseable payload records nothing",
			payload: `not json`,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, rec := newTestHandler()
			h.recordSESNotification(context.Background(), "t", "p", []byte(tt.payload))

			if len(rec.events) != len(tt.want) {
				t.Fatalf("recorded %d events (%+v), want %d", len(rec.events), rec.events, len(tt.want))
			}
			for i, want := range tt.want {
				got := rec.events[i]
				if got.messageID != want.messageID || got.eventType != want.eventType ||
					got.recipient != want.recipient || got.errorMessage != want.errorMessage {
					t.Errorf("event %d = %+v, want %+v", i, got, want)
				}
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHandleSESConfirmsASubscription(t *testing.T) {
	h, _ := newTestHandler()

	var visited string
	h.snsHTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		visited = r.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     http.Header{},
		}, nil
	})}

	const subscribeURL = "https://sns.us-east-1.amazonaws.com/?Action=ConfirmSubscription&Token=abc"
	body := `{"Type":"SubscriptionConfirmation","SubscribeURL":"` + subscribeURL + `"}`

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/webhooks/t/p/ses", strings.NewReader(body))
	h.handleSES(w, r, "t", "p", []byte(body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if visited != subscribeURL {
		t.Fatalf("confirmed %q, want %q", visited, subscribeURL)
	}
}

// A signed message from an allowed topic can still carry a SubscribeURL
// pointing anywhere. Confirming it would turn the gateway into a request
// forwarder, so the fetch must not happen at all.
func TestHandleSESRefusesAForeignSubscribeURL(t *testing.T) {
	h, _ := newTestHandler()

	fetched := false
	h.snsHTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		fetched = true
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}

	body := `{"Type":"SubscriptionConfirmation","SubscribeURL":"https://evil.test/steal"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/webhooks/t/p/ses", strings.NewReader(body))
	h.handleSES(w, r, "t", "p", []byte(body))

	if fetched {
		t.Fatal("fetched a SubscribeURL that is not an SNS endpoint")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleSESRecordsANotification(t *testing.T) {
	h, rec := newTestHandler()

	// The SES payload arrives as a JSON string inside the SNS envelope.
	inner := `{\"notificationType\":\"Bounce\",\"mail\":{\"messageId\":\"m-1\"},` +
		`\"bounce\":{\"bounceType\":\"Permanent\",\"bouncedRecipients\":[{\"emailAddress\":\"dead@example.com\"}]}}`
	body := `{"Type":"Notification","Message":"` + inner + `"}`

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/webhooks/t/p/ses", strings.NewReader(body))
	h.handleSES(w, r, "t", "p", []byte(body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(rec.events) != 1 || rec.events[0].eventType != panmailv1.EmailEventType_EMAIL_EVENT_TYPE_HARD_BOUNCE {
		t.Fatalf("got %+v, want one hard bounce", rec.events)
	}
}

// The topic check runs before any signature work, so a message from a topic
// the provider is not configured for is refused without a network call.
func TestVerifySNSRefusesAnUnconfiguredTopic(t *testing.T) {
	h, _ := newTestHandler()
	body := `{"Type":"Notification","TopicArn":"arn:aws:sns:us-east-1:1:someone-elses"}`

	err := h.verifySNS(context.Background(), "arn:aws:sns:us-east-1:1:mine", []byte(body))
	if err == nil {
		t.Fatal("accepted a message from an unconfigured topic")
	}
	if !IsVerificationError(err) {
		t.Fatalf("error = %v, want a verification error", err)
	}
}

func TestVerifySNSRefusesAMalformedBody(t *testing.T) {
	h, _ := newTestHandler()
	if err := h.verifySNS(context.Background(), "arn:aws:sns:us-east-1:1:mine", []byte("not json")); err == nil {
		t.Fatal("accepted an unparseable body")
	}
}
