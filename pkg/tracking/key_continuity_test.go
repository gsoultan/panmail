package tracking_test

import (
	"testing"

	"github.com/gsoultan/panmail/pkg/tracking"
)

// A link has to verify under the key the next process derives, not just under
// the one the current process happens to hold.
//
// On a fresh install the signer is built before there is a symmetric key to
// derive from. Setup generates one later in the same process. If the signer is
// not moved to it, every link minted between those two moments verifies happily
// until the process restarts — and then never again, because the restart reads
// the real key from the config file. Those messages are in inboxes by then, so
// there is nothing to reissue: clicks answer 403 and opens are dropped forever.
//
// This walks that timeline.
func TestLinksSurviveTheKeyArrivingDuringSetup(t *testing.T) {
	const symmetricKey = "8d969eef6ecad3c29a3a629280e686cf" // what setup generates

	link := tracking.Link{
		Kind:      "click",
		TenantID:  "11111111-1111-1111-1111-111111111111",
		MessageID: "msg-1",
		Recipient: "person+tag@example.org",
		TargetURL: "https://shop.example.com/sale",
	}

	// Boot before setup: no key yet.
	signer := tracking.NewSigner(nil)
	if signer.HasKey() {
		t.Fatal("a signer built before setup reports a key it cannot have")
	}
	if sig := signer.Sign(link); sig != "" {
		t.Fatalf("an unkeyed signer minted %q; the send path would embed a link "+
			"that stops verifying at the next restart", sig)
	}

	// Setup runs and hands the signer the key it just generated.
	signer.SetKey(tracking.DeriveKey(symmetricKey))

	// A message goes out now, signed by this still-running process.
	signature := signer.Sign(link)
	if signature == "" {
		t.Fatal("no signature after setup supplied a key")
	}

	// panmail restarts and derives the key from the config file setup wrote.
	afterRestart := tracking.NewSigner(tracking.DeriveKey(symmetricKey))

	if err := afterRestart.Verify(link, signature); err != nil {
		t.Fatalf("a link sent between setup and the restart no longer verifies: %v\n"+
			"every recipient who clicks gets \"Invalid tracking link\", and the "+
			"message cannot be reissued", err)
	}
}

// The key has to be what makes it verify. If an unkeyed signer accepted
// anything, the click endpoint would be an open redirect before setup.
func TestAnUnkeyedSignerVerifiesNothing(t *testing.T) {
	link := tracking.Link{Kind: "click", TenantID: "t", MessageID: "m", TargetURL: "https://example.com"}

	keyed := tracking.NewSigner(tracking.DeriveKey("some-symmetric-key"))
	signature := keyed.Sign(link)

	unkeyed := tracking.NewSigner(nil)
	if err := unkeyed.Verify(link, signature); err == nil {
		t.Error("a signer with no key accepted a signature; the redirect endpoint " +
			"would be open to anyone before setup finished")
	}
	if err := unkeyed.Verify(link, "not-a-signature"); err == nil {
		t.Error("a signer with no key accepted an invented signature")
	}
}

// Different symmetric keys must not produce interchangeable links.
func TestDeriveKeySeparatesInstances(t *testing.T) {
	link := tracking.Link{Kind: "open", TenantID: "t", MessageID: "m"}

	a := tracking.NewSigner(tracking.DeriveKey("instance-a-symmetric-key"))
	b := tracking.NewSigner(tracking.DeriveKey("instance-b-symmetric-key"))

	if err := b.Verify(link, a.Sign(link)); err == nil {
		t.Error("a link signed by one instance verified at another")
	}
}
