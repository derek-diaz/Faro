package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const KeyBytes = 32

func LoadOrCreateKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) != KeyBytes {
			return nil, errors.New("Faro's secret key has an invalid length")
		}
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	key := make([]byte, KeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func Encrypt(key, plaintext []byte) (string, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, plaintext, []byte("faro-secret-v1"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func Decrypt(key []byte, encoded string) ([]byte, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("stored secret is invalid")
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("stored secret is invalid")
	}
	plaintext, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte("faro-secret-v1"))
	if err != nil {
		return nil, fmt.Errorf("decrypt stored secret: %w", err)
	}
	return plaintext, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != KeyBytes {
		return nil, errors.New("Faro's secret key has an invalid length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
