package redundancy

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

const pairingPrefix = "FARO1"

type parsedPairingCode struct {
	ID            string
	Token         []byte
	ControllerKey []byte
}

func randomBytes(size int) ([]byte, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return nil, err
	}
	return value, nil
}

func randomID(bytes int) (string, error) {
	value, err := randomBytes(bytes)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func encodePairingCode(id string, token, publicKey []byte) string {
	return strings.Join([]string{
		pairingPrefix,
		id,
		base64.RawURLEncoding.EncodeToString(token),
		base64.RawURLEncoding.EncodeToString(publicKey),
	}, ".")
}

func parsePairingCode(value string) (parsedPairingCode, error) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 4 || parts[0] != pairingPrefix || len(parts[1]) != 16 {
		return parsedPairingCode{}, errors.New("pairing code is invalid")
	}
	token, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(token) != 32 {
		return parsedPairingCode{}, errors.New("pairing code is invalid")
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(publicKey) != 32 {
		return parsedPairingCode{}, errors.New("pairing code is invalid")
	}
	return parsedPairingCode{ID: parts[1], Token: token, ControllerKey: publicKey}, nil
}

func pairingProof(token []byte, request PairRequest) []byte {
	return hmacBytes(token, []byte(strings.Join([]string{
		request.PairingID,
		request.NodeID,
		request.NodeName,
		request.LANAddress,
		request.PublicKey,
	}, "\n")))
}

func pairingKey(privateKey, remotePublicKey, token []byte) ([]byte, error) {
	curve := ecdh.X25519()
	private, err := curve.NewPrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	public, err := curve.NewPublicKey(remotePublicKey)
	if err != nil {
		return nil, err
	}
	shared, err := private.ECDH(public)
	if err != nil {
		return nil, err
	}
	reader := hkdf.New(sha256.New, shared, token, []byte("faro-redundancy-pairing-v1"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

func sealEnvelope(key, plaintext []byte, purpose string) (encryptedEnvelope, error) {
	aead, err := redundancyAEAD(key)
	if err != nil {
		return encryptedEnvelope{}, err
	}
	nonce, err := randomBytes(aead.NonceSize())
	if err != nil {
		return encryptedEnvelope{}, err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, []byte(purpose))
	return encryptedEnvelope{
		Nonce:      base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext),
	}, nil
}

func openEnvelope(key []byte, envelope encryptedEnvelope, purpose string) ([]byte, error) {
	aead, err := redundancyAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil || len(nonce) != aead.NonceSize() {
		return nil, errors.New("encrypted redundancy response is invalid")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, errors.New("encrypted redundancy response is invalid")
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(purpose))
	if err != nil {
		return nil, errors.New("encrypted redundancy response could not be authenticated")
	}
	return plaintext, nil
}

func redundancyAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("redundancy secret has an invalid length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func hmacBytes(key, message []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}

func signRequest(request *http.Request, body, secret []byte, now time.Time) error {
	nonce, err := randomBytes(16)
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(now.UTC().Unix(), 10)
	nonceText := base64.RawURLEncoding.EncodeToString(nonce)
	signature := requestSignature(secret, request.Method, request.URL.RequestURI(), timestamp, nonceText, body)
	request.Header.Set("X-Faro-Timestamp", timestamp)
	request.Header.Set("X-Faro-Nonce", nonceText)
	request.Header.Set("X-Faro-Signature", base64.RawURLEncoding.EncodeToString(signature))
	return nil
}

func requestSignature(secret []byte, method, requestURI, timestamp, nonce string, body []byte) []byte {
	bodyHash := sha256.Sum256(body)
	message := strings.Join([]string{
		method,
		requestURI,
		timestamp,
		nonce,
		hex.EncodeToString(bodyHash[:]),
	}, "\n")
	return hmacBytes(secret, []byte(message))
}

func decodeSignature(value string) ([]byte, error) {
	signature, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(signature) != sha256.Size {
		return nil, fmt.Errorf("invalid redundancy signature")
	}
	return signature, nil
}
