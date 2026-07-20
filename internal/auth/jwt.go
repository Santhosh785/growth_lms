package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"growth-lms/internal/config"
)

// ErrInvalidToken is returned for any token that fails signature
// verification, is expired, or is otherwise malformed. Callers should
// treat it as a generic 401 and must not leak the underlying reason to
// the client.
var ErrInvalidToken = errors.New("auth: invalid token")

// Claims is the subset of a Supabase-issued access token this backend
// relies on.
type Claims struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
	jwt.RegisteredClaims
}

// Verifier validates Supabase-issued JWTs. Supabase projects sign access
// tokens either with HS256 using the project's shared JWT secret (legacy
// projects, "Project Settings -> API -> JWT Settings") or, on projects
// using Supabase's newer JWT signing keys feature (the default for new
// projects and for the local CLI), with an asymmetric key (ES256/RS256)
// published at the project's JWKS endpoint. Both are supported here so
// this works against either kind of project without configuration.
type Verifier struct {
	secret  []byte
	jwksURL string
	http    *http.Client

	mu        sync.RWMutex
	keys      map[string]interface{} // kid -> *ecdsa.PublicKey / *rsa.PublicKey
	fetchedAt time.Time
}

const jwksMinRefetchInterval = 10 * time.Second

// NewVerifier builds a Verifier from the application's Supabase config.
func NewVerifier(cfg config.SupabaseConfig) (*Verifier, error) {
	if cfg.JWTSecret == "" {
		return nil, errors.New("auth: supabase JWT secret is not configured")
	}
	return &Verifier{
		secret:  []byte(cfg.JWTSecret),
		jwksURL: strings.TrimRight(cfg.URL, "/") + "/auth/v1/.well-known/jwks.json",
		http:    &http.Client{Timeout: 5 * time.Second},
		keys:    map[string]interface{}{},
	}, nil
}

// Verify parses and validates a raw JWT string, returning its claims on
// success. The expected signing method is derived from the token's own
// `alg` header and dispatched to the matching verification path (HMAC
// against the shared secret, or ECDSA/RSA against a JWKS-published
// public key) — never the other way around, which is what prevents an
// alg-confusion attack (e.g. an attacker can't take a public EC key and
// present it as if it were an HMAC secret, since the HMAC path only ever
// uses our own configured secret, and the asymmetric path only ever uses
// keys fetched from our own trusted JWKS endpoint).
func (v *Verifier) Verify(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		switch t.Method.(type) {
		case *jwt.SigningMethodHMAC:
			return v.secret, nil
		case *jwt.SigningMethodECDSA, *jwt.SigningMethodRSA:
			kid, _ := t.Header["kid"].(string)
			if kid == "" {
				return nil, fmt.Errorf("auth: token missing kid")
			}
			return v.publicKey(kid)
		default:
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	if claims.Subject == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// publicKey returns the public key for kid, fetching (or re-fetching,
// on a cache miss) the project's JWKS document as needed. Refetches are
// throttled so that requests bearing an unknown/bogus kid can't be used
// to force a JWKS fetch per request.
func (v *Verifier) publicKey(kid string) (interface{}, error) {
	v.mu.RLock()
	key, ok := v.keys[kid]
	fetchedAt := v.fetchedAt
	v.mu.RUnlock()
	if ok {
		return key, nil
	}
	if time.Since(fetchedAt) < jwksMinRefetchInterval {
		return nil, fmt.Errorf("auth: unknown signing key %q", kid)
	}
	if err := v.refreshJWKS(); err != nil {
		return nil, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	key, ok = v.keys[kid]
	if !ok {
		return nil, fmt.Errorf("auth: unknown signing key %q", kid)
	}
	return key, nil
}

type jwks struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (v *Verifier) refreshJWKS() error {
	resp, err := v.http.Get(v.jwksURL)
	if err != nil {
		return fmt.Errorf("auth: fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("auth: fetch jwks: unexpected status %d", resp.StatusCode)
	}
	var doc jwks
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("auth: decode jwks: %w", err)
	}

	keys := make(map[string]interface{}, len(doc.Keys))
	for _, k := range doc.Keys {
		pub, err := k.publicKey()
		if err != nil {
			continue // skip key types we don't support (e.g. "oct")
		}
		keys[k.Kid] = pub
	}

	v.mu.Lock()
	v.keys = keys
	v.fetchedAt = time.Now()
	v.mu.Unlock()
	return nil
}

func (k jwk) publicKey() (interface{}, error) {
	switch k.Kty {
	case "EC":
		curve, err := ellipticCurve(k.Crv)
		if err != nil {
			return nil, err
		}
		x, err := base64URLBigInt(k.X)
		if err != nil {
			return nil, err
		}
		y, err := base64URLBigInt(k.Y)
		if err != nil {
			return nil, err
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	case "RSA":
		n, err := base64URLBigInt(k.N)
		if err != nil {
			return nil, err
		}
		e, err := base64URLBigInt(k.E)
		if err != nil {
			return nil, err
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	default:
		return nil, fmt.Errorf("auth: unsupported jwk kty %q", k.Kty)
	}
}

func ellipticCurve(crv string) (elliptic.Curve, error) {
	switch crv {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("auth: unsupported jwk crv %q", crv)
	}
}

func base64URLBigInt(s string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("auth: decode jwk field: %w", err)
	}
	return new(big.Int).SetBytes(b), nil
}
