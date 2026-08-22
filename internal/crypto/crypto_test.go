package crypto

import (
	"bytes"
	"testing"
)

// testIterations keeps the suite fast. Production uses DefaultIterations.
const testIterations = 1000

func testKey(t *testing.T, master string) *Key {
	t.Helper()
	k, err := DeriveKey(master, []byte("0123456789abcdef"), testIterations)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	k := testKey(t, "correct horse battery staple")
	plaintext := []byte("sk-live-abcdef")
	ct, nonce, err := k.Seal(plaintext, []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, plaintext) {
		t.Fatal("ciphertext contains the plaintext")
	}
	got, err := k.Open(ct, nonce, []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round trip = %q, want %q", got, plaintext)
	}
}

func TestSealUsesAFreshNonceEveryTime(t *testing.T) {
	k := testKey(t, "master")
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		ct, nonce, err := k.Seal([]byte("same plaintext"), []byte("row-1"))
		if err != nil {
			t.Fatal(err)
		}
		if seen[string(nonce)] {
			t.Fatal("nonce reused; GCM loses all confidentiality on nonce reuse")
		}
		seen[string(nonce)] = true
		if seen[string(ct)] {
			t.Fatal("identical ciphertext for identical plaintext")
		}
		seen[string(ct)] = true
	}
}

// A ciphertext moved from one credential row to another must fail to
// authenticate. Without AAD binding, an attacker with write access to the
// database file could promote a low-privilege key into a high-privilege row.
func TestOpenRejectsACiphertextSwappedBetweenRows(t *testing.T) {
	k := testKey(t, "master")
	ct, nonce, err := k.Seal([]byte("secret"), []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Open(ct, nonce, []byte("row-2")); err == nil {
		t.Fatal("expected a different AAD to fail authentication")
	}
}

func TestOpenRejectsTheWrongMasterKey(t *testing.T) {
	good := testKey(t, "right")
	bad := testKey(t, "wrong")
	ct, nonce, err := good.Seal([]byte("secret"), []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Open(ct, nonce, []byte("row-1")); err == nil {
		t.Fatal("expected the wrong key to fail authentication")
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	k := testKey(t, "master")
	ct, nonce, err := k.Seal([]byte("secret"), []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	ct[0] ^= 0xff
	if _, err := k.Open(ct, nonce, []byte("row-1")); err == nil {
		t.Fatal("expected a flipped bit to fail authentication")
	}
}

func TestDeriveKeyIsDeterministic(t *testing.T) {
	salt := []byte("0123456789abcdef")
	a, err := DeriveKey("master", salt, testIterations)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DeriveKey("master", salt, testIterations)
	if err != nil {
		t.Fatal(err)
	}
	ct, nonce, err := a.Seal([]byte("secret"), []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	// Determinism is what makes a restart able to read yesterday's credentials.
	if _, err := b.Open(ct, nonce, []byte("row-1")); err != nil {
		t.Fatalf("a second derivation could not open the first's output: %v", err)
	}
}

func TestDeriveKeyRejectsEmptyInput(t *testing.T) {
	salt := []byte("0123456789abcdef")
	if _, err := DeriveKey("", salt, testIterations); err == nil {
		t.Error("expected an empty master key to be rejected")
	}
	if _, err := DeriveKey("master", nil, testIterations); err == nil {
		t.Error("expected an empty salt to be rejected")
	}
	if _, err := DeriveKey("master", salt, 0); err == nil {
		t.Error("expected a zero iteration count to be rejected")
	}
}

func TestNewSaltIsRandomAndCorrectLength(t *testing.T) {
	a, err := NewSalt()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != SaltBytes {
		t.Fatalf("salt length = %d, want %d", len(a), SaltBytes)
	}
	b, err := NewSalt()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two salts were identical")
	}
}
