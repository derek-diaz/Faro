package secrets

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestStoredSecretRoundTripAndAuthentication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets", "key")
	key, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, again) {
		t.Fatal("secret key changed after it was persisted")
	}

	ciphertext, err := Encrypt(key, []byte("replica-secret"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "replica-secret" {
		t.Fatalf("decrypted secret = %q", plaintext)
	}

	wrongKey := bytes.Repeat([]byte{0x42}, KeyBytes)
	if _, err := Decrypt(wrongKey, ciphertext); err == nil {
		t.Fatal("ciphertext was accepted with the wrong key")
	}
}
