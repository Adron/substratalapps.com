package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// Signer produces RS256 signatures. Production signs with KMS (the private
// key never leaves it, Auth → Signing keys); local and tests use a PEM key.
type Signer interface {
	// KeyID is the kid of the key that signs new tokens.
	KeyID() string
	// SignDigest signs a SHA-256 digest with RSASSA-PKCS1-v1_5.
	SignDigest(ctx context.Context, digest []byte) ([]byte, error)
	// PublicKeys are every key to publish in the JWKS and accept when
	// verifying: the active one plus any being rotated in or out.
	PublicKeys() map[string]*rsa.PublicKey
}

// KeyIDFor derives a stable kid from a public key.
func KeyIDFor(pub *rsa.PublicKey) string {
	der, _ := x509.MarshalPKIXPublicKey(pub)
	sum := sha256.Sum256(der)
	return "sk_" + hex.EncodeToString(sum[:6])
}

// LocalSigner signs with an in-process RSA key.
type LocalSigner struct {
	key *rsa.PrivateKey
	kid string
}

// NewLocalSigner loads the PEM key at path, generating it on first use.
func NewLocalSigner(path string) (*LocalSigner, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key, gerr := rsa.GenerateKey(rand.Reader, 2048)
		if gerr != nil {
			return nil, gerr
		}
		b = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("auth: %s isn't a PEM key", path)
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return NewLocalSignerFromKey(key), nil
}

// NewLocalSignerFromKey wraps an existing key (tests).
func NewLocalSignerFromKey(key *rsa.PrivateKey) *LocalSigner {
	return &LocalSigner{key: key, kid: KeyIDFor(&key.PublicKey)}
}

func (s *LocalSigner) KeyID() string { return s.kid }

func (s *LocalSigner) SignDigest(_ context.Context, digest []byte) ([]byte, error) {
	return rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest)
}

func (s *LocalSigner) PublicKeys() map[string]*rsa.PublicKey {
	return map[string]*rsa.PublicKey{s.kid: &s.key.PublicKey}
}

// KMSSigner signs with an asymmetric RSA_2048 KMS key.
type KMSSigner struct {
	client *kms.Client
	keyID  string // KMS key id/ARN that signs
	kid    string
	pubs   map[string]*rsa.PublicKey
}

// NewKMSSigner fetches the public halves of the active key and of every
// additionally published key (rotation, Auth → Signing keys) once, at cold
// start, so verification never calls KMS.
func NewKMSSigner(ctx context.Context, client *kms.Client, active string, published []string) (*KMSSigner, error) {
	s := &KMSSigner{client: client, keyID: active, pubs: map[string]*rsa.PublicKey{}}
	for i, id := range append([]string{active}, published...) {
		out, err := client.GetPublicKey(ctx, &kms.GetPublicKeyInput{KeyId: &id})
		if err != nil {
			return nil, fmt.Errorf("auth: kms public key %s: %w", id, err)
		}
		pk, err := x509.ParsePKIXPublicKey(out.PublicKey)
		if err != nil {
			return nil, err
		}
		rpk, ok := pk.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("auth: kms key %s isn't RSA", id)
		}
		kid := KeyIDFor(rpk)
		s.pubs[kid] = rpk
		if i == 0 {
			s.kid = kid
		}
	}
	return s, nil
}

func (s *KMSSigner) KeyID() string { return s.kid }

func (s *KMSSigner) SignDigest(ctx context.Context, digest []byte) ([]byte, error) {
	out, err := s.client.Sign(ctx, &kms.SignInput{
		KeyId:            &s.keyID,
		Message:          digest,
		MessageType:      kmstypes.MessageTypeDigest,
		SigningAlgorithm: kmstypes.SigningAlgorithmSpecRsassaPkcs1V15Sha256,
	})
	if err != nil {
		return nil, err
	}
	return out.Signature, nil
}

func (s *KMSSigner) PublicKeys() map[string]*rsa.PublicKey { return s.pubs }

var b64 = base64.RawURLEncoding

// SignJWT encodes and signs claims as an RS256 JWT.
func SignJWT(ctx context.Context, s Signer, claims any) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": s.KeyID()})
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := b64.EncodeToString(header) + "." + b64.EncodeToString(body)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := s.SignDigest(ctx, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + b64.EncodeToString(sig), nil
}

// ErrInvalidToken is any verification failure; callers return 401.
var ErrInvalidToken = errors.New("auth: invalid token")

// VerifyJWT checks an RS256 JWT's signature against keys and decodes its
// claims into dst. Callers check iss/aud/exp on the decoded claims.
func VerifyJWT(token string, keys map[string]*rsa.PublicKey, dst any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ErrInvalidToken
	}
	hb, err := b64.DecodeString(parts[0])
	if err != nil {
		return ErrInvalidToken
	}
	var h struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(hb, &h) != nil || h.Alg != "RS256" {
		return ErrInvalidToken
	}
	pub, ok := keys[h.Kid]
	if !ok {
		return ErrInvalidToken
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return ErrInvalidToken
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig) != nil {
		return ErrInvalidToken
	}
	body, err := b64.DecodeString(parts[1])
	if err != nil {
		return ErrInvalidToken
	}
	if json.Unmarshal(body, dst) != nil {
		return ErrInvalidToken
	}
	return nil
}

// JWK is one published key.
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKS renders keys as a JSON Web Key Set.
func JWKS(keys map[string]*rsa.PublicKey) map[string][]JWK {
	out := make([]JWK, 0, len(keys))
	for kid, k := range keys {
		out = append(out, JWK{
			Kty: "RSA", Kid: kid, Use: "sig", Alg: "RS256",
			N: b64.EncodeToString(k.N.Bytes()),
			E: b64.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
		})
	}
	return map[string][]JWK{"keys": out}
}

// Now is overridable in tests.
var Now = time.Now
