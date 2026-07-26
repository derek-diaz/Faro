package unifi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const secretKeyBytes = 32

func loadOrCreateKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) != secretKeyBytes {
			return nil, errors.New("Faro's integration secret key has an invalid length")
		}
		return data, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	key := make([]byte, secretKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, key, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return nil, err
	}
	return key, nil
}

func encryptSecret(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), []byte("faro:integration:unifi:v1"))
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func decryptSecret(key []byte, encoded string) (string, error) {
	data, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("stored UniFi credentials are invalid")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < aead.NonceSize() {
		return "", errors.New("stored UniFi credentials are invalid")
	}
	plaintext, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte("faro:integration:unifi:v1"))
	if err != nil {
		return "", fmt.Errorf("decrypt stored UniFi credentials: %w", err)
	}
	return string(plaintext), nil
}
