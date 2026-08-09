package secrets

import (
	"strings"
	"testing"
)

// Rotation is the point of all this: before it, a suspected key compromise had
// no remedy short of re-entering every credential by hand.

func mustKey(t *testing.T) string {
	t.Helper()
	k, err := GenerateKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return k
}

func mustRing(t *testing.T, primary string, retired ...string) *Keyring {
	t.Helper()
	k, err := NewKeyring(primary, retired...)
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return k
}

// The one that would silently break every existing deployment.
func TestValuesWrittenBeforeKeyIdentityStillDecrypt(t *testing.T) {
	key := mustKey(t)
	ring := mustRing(t, key)

	// Exactly what the previous version produced: no key id in the marker.
	v2, err := ring.Encrypt("hunter2")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	payload := v2[strings.LastIndex(v2, ":")+1:]
	v1 := cipherPrefixV1 + payload

	got, err := ring.Decrypt(v1)
	if err != nil {
		t.Fatalf("decrypt v1: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("decrypted %q, want the original", got)
	}
}

func TestNewValuesNameTheKeyThatWroteThem(t *testing.T) {
	ring := mustRing(t, mustKey(t))

	sealed, err := ring.Encrypt("secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(sealed, cipherPrefixV2+ring.PrimaryKeyID()+":") {
		t.Errorf("ciphertext %q does not name the primary key", sealed[:24])
	}
}

func TestARetiredKeyStillReadsWhatItWrote(t *testing.T) {
	oldKey, newKey := mustKey(t), mustKey(t)

	before := mustRing(t, oldKey)
	sealed, err := before.Encrypt("dkim-private-key")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// After the rotation: new key primary, old key kept for reading.
	after := mustRing(t, newKey, oldKey)
	got, err := after.Decrypt(sealed)
	if err != nil {
		t.Fatalf("decrypt after rotation: %v", err)
	}
	if got != "dkim-private-key" {
		t.Errorf("decrypted %q, want the original", got)
	}
}

// Dropping the old key too early is the mistake that loses credentials, so the
// error has to say what to do rather than just fail.
func TestAnUnknownKeyIsReportedActionably(t *testing.T) {
	sealed, err := mustRing(t, mustKey(t)).Encrypt("secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	_, err = mustRing(t, mustKey(t)).Decrypt(sealed)
	if err == nil {
		t.Fatal("decrypting with the wrong key should fail")
	}
	if !strings.Contains(err.Error(), EnvRetiredKeysName) {
		t.Errorf("error %q does not say how to recover", err)
	}
}

func TestRotationIsFinishable(t *testing.T) {
	oldKey, newKey := mustKey(t), mustKey(t)

	sealed, err := mustRing(t, oldKey).Encrypt("smtp-password")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	after := mustRing(t, newKey, oldKey)

	// Without knowing which values still need moving there is no way to tell
	// when the old key can safely be dropped.
	if !after.NeedsRotation(sealed) {
		t.Error("a value written with the retired key should be flagged for rotation")
	}

	plaintext, err := after.Decrypt(sealed)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	rewritten, err := after.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("re-encrypt: %v", err)
	}

	if after.NeedsRotation(rewritten) {
		t.Error("a value rewritten under the primary key is still flagged")
	}
	// And the old key is now genuinely unnecessary.
	final := mustRing(t, newKey)
	if got, err := final.Decrypt(rewritten); err != nil || got != "smtp-password" {
		t.Errorf("after rotation the value should read without the old key; got %q, %v", got, err)
	}
}

func TestAValueOnTheCurrentKeyNeedsNoRotation(t *testing.T) {
	ring := mustRing(t, mustKey(t))
	sealed, _ := ring.Encrypt("secret")
	if ring.NeedsRotation(sealed) {
		t.Error("a freshly written value was flagged for rotation")
	}
}

// v1 has no key identity, so it always wants rewriting even when the key has
// not changed.
func TestAnUnidentifiedValueNeedsRotation(t *testing.T) {
	ring := mustRing(t, mustKey(t))
	sealed, _ := ring.Encrypt("secret")
	v1 := cipherPrefixV1 + sealed[strings.LastIndex(sealed, ":")+1:]

	if !ring.NeedsRotation(v1) {
		t.Error("a value with no key identity should be rewritten")
	}
}

func TestPlaintextIsNotMistakenForSomethingToRotate(t *testing.T) {
	ring := mustRing(t, mustKey(t))
	if ring.NeedsRotation("plain-old-password") {
		t.Error("an unencrypted value was flagged; it is upgraded when next saved, not rotated")
	}
	if ring.NeedsRotation("") {
		t.Error("an empty value was flagged")
	}
}

// Re-running a rotation with the same key on both sides must not break, or a
// rerun of the deploy step is dangerous.
func TestListingThePrimaryAsRetiredIsHarmless(t *testing.T) {
	key := mustKey(t)
	ring := mustRing(t, key, key)

	sealed, err := ring.Encrypt("secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if got, err := ring.Decrypt(sealed); err != nil || got != "secret" {
		t.Errorf("got %q, %v", got, err)
	}
	if ring.NeedsRotation(sealed) {
		t.Error("value flagged for rotation when nothing changed")
	}
}

func TestSeveralRetiredKeysAreAllTried(t *testing.T) {
	k1, k2, k3 := mustKey(t), mustKey(t), mustKey(t)

	fromK1, _ := mustRing(t, k1).Encrypt("one")
	fromK2, _ := mustRing(t, k2).Encrypt("two")

	ring := mustRing(t, k3, k1, k2)
	if got, err := ring.Decrypt(fromK1); err != nil || got != "one" {
		t.Errorf("k1 value: got %q, %v", got, err)
	}
	if got, err := ring.Decrypt(fromK2); err != nil || got != "two" {
		t.Errorf("k2 value: got %q, %v", got, err)
	}
}

func TestParseRetiredKeys(t *testing.T) {
	got := ParseRetiredKeys(" aaa , bbb ,, ccc ")
	want := []string{"aaa", "bbb", "ccc"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
			break
		}
	}
	if len(ParseRetiredKeys("")) != 0 {
		t.Error("an empty setting should yield no keys")
	}
}

func TestAMalformedRetiredKeyIsRejectedAtStartup(t *testing.T) {
	// Better to refuse to start than to run with a key that silently cannot
	// read half the credentials.
	if _, err := NewKeyring(mustKey(t), "not-a-key"); err == nil {
		t.Error("a malformed retired key should be rejected")
	}
}
