package tracking

import "testing"

func TestABounceReferenceVerifiesOnlyForWhatItWasMintedFor(t *testing.T) {
	s := NewSigner([]byte("k"))
	ref := s.BounceRef("tenant-a", "msg-1", "Person@Example.org")

	if id, err := s.VerifyBounceRef(ref, "tenant-a", "person@example.org"); err != nil || id != "msg-1" {
		t.Fatalf("VerifyBounceRef = (%q, %v), want msg-1", id, err)
	}

	refused := map[string]struct{ value, tenant, recipient string }{
		"another tenant":    {ref, "tenant-b", "person@example.org"},
		"another recipient": {ref, "tenant-a", "victim@example.org"},
		"another message":   {"v1.msg-2" + ref[len("v1.msg-1"):], "tenant-a", "person@example.org"},
		"another kind":      {"v1.msg-1." + s.Sign(Link{Kind: "unsubscribe", TenantID: "tenant-a", MessageID: "msg-1", Recipient: "person@example.org"}), "tenant-a", "person@example.org"},
		"no signature":      {"v1.msg-1", "tenant-a", "person@example.org"},
		"no version":        {ref[len("v1."):], "tenant-a", "person@example.org"},
	}
	for name, tc := range refused {
		if _, err := s.VerifyBounceRef(tc.value, tc.tenant, tc.recipient); err == nil {
			t.Errorf("%s: a reference verified that should not", name)
		}
	}
}

func TestNoBounceReferenceWithoutAKey(t *testing.T) {
	if ref := NewSigner(nil).BounceRef("t", "m", "r@example.org"); ref != "" {
		t.Errorf("minted %q with no key", ref)
	}
}
