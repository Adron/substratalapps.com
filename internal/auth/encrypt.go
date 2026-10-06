package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// Encrypter protects secrets the server must be able to read back: TOTP
// secrets and webhook signing secrets (NFR → Secrets at rest). KMS in
// production; AES-256-GCM under a local key elsewhere.
type Encrypter interface {
	Encrypt(ctx context.Context, plaintext string) (string, error)
	Decrypt(ctx context.Context, ciphertext string) (string, error)
}

// LocalEncrypter is AES-256-GCM. Ciphertext is "l1:" + base64(nonce|sealed).
type LocalEncrypter struct{ aead cipher.AEAD }

// NewLocalEncrypter uses a base64 32-byte key, or derives a fixed
// development key when key is empty (never valid in production; config
// refuses to start production without KMS).
func NewLocalEncrypter(key string) (*LocalEncrypter, error) {
	var k []byte
	if key == "" {
		sum := sha256.Sum256([]byte("substratal-local-development-data-key"))
		k = sum[:]
	} else {
		var err error
		if k, err = base64.StdEncoding.DecodeString(key); err != nil || len(k) != 32 {
			return nil, fmt.Errorf("auth: LOCAL_DATA_KEY must be base64 of 32 bytes")
		}
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &LocalEncrypter{aead: aead}, nil
}

func (e *LocalEncrypter) Encrypt(_ context.Context, plaintext string) (string, error) {
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := e.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return "l1:" + base64.StdEncoding.EncodeToString(sealed), nil
}

func (e *LocalEncrypter) Decrypt(_ context.Context, ciphertext string) (string, error) {
	if len(ciphertext) < 3 || ciphertext[:3] != "l1:" {
		return "", errors.New("auth: not a local ciphertext")
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext[3:])
	if err != nil || len(raw) < e.aead.NonceSize() {
		return "", errors.New("auth: malformed ciphertext")
	}
	pt, err := e.aead.Open(nil, raw[:e.aead.NonceSize()], raw[e.aead.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// KMSEncrypter encrypts directly with a symmetric KMS key. Every secret it
// protects is far below KMS's 4 KB direct-encryption limit.
type KMSEncrypter struct {
	client *kms.Client
	keyID  string
}

// NewKMSEncrypter returns a KMS-backed Encrypter.
func NewKMSEncrypter(client *kms.Client, keyID string) *KMSEncrypter {
	return &KMSEncrypter{client: client, keyID: keyID}
}

func (e *KMSEncrypter) Encrypt(ctx context.Context, plaintext string) (string, error) {
	out, err := e.client.Encrypt(ctx, &kms.EncryptInput{KeyId: &e.keyID, Plaintext: []byte(plaintext)})
	if err != nil {
		return "", err
	}
	return "k1:" + base64.StdEncoding.EncodeToString(out.CiphertextBlob), nil
}

func (e *KMSEncrypter) Decrypt(ctx context.Context, ciphertext string) (string, error) {
	if len(ciphertext) < 3 || ciphertext[:3] != "k1:" {
		return "", errors.New("auth: not a KMS ciphertext")
	}
	blob, err := base64.StdEncoding.DecodeString(ciphertext[3:])
	if err != nil {
		return "", err
	}
	out, err := e.client.Decrypt(ctx, &kms.DecryptInput{KeyId: &e.keyID, CiphertextBlob: blob})
	if err != nil {
		return "", err
	}
	return string(out.Plaintext), nil
}
