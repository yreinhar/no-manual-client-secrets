package main

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var (
	jwksURL  = getEnv("JWKS_URL", "http://keycloak.keycloak.svc.cluster.local/realms/kubernetes/protocol/openid-connect/certs")
	issuer   = getEnv("ISSUER", "http://keycloak.example.com/realms/kubernetes")
	audience = getEnv("AUDIENCE", "service-b")
	addr     = getEnv("ADDR", ":8080")
)

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksResponse struct {
	Keys []jwk `json:"keys"`
}

// avoid to fetch the JWKS on every single request
type jwksCache struct {
	mu      sync.RWMutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

func b64URLDecode(s string) ([]byte, error) {
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.StdEncoding.DecodeString(s)
}

func (c *jwksCache) refresh() error {
	resp, err := http.Get(jwksURL) //nolint:noctx
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read JWKS body: %w", err)
	}

	var set jwksResponse
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("parse JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Use != "sig" {
			continue
		}
		nBytes, err := b64URLDecode(k.N)
		if err != nil {
			return fmt.Errorf("decode n for kid %s: %w", k.Kid, err)
		}
		eBytes, err := b64URLDecode(k.E)
		if err != nil {
			return fmt.Errorf("decode e for kid %s: %w", k.Kid, err)
		}
		keys[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: int(new(big.Int).SetBytes(eBytes).Int64()),
		}
	}

	c.mu.Lock()
	c.keys = keys
	c.fetched = time.Now()
	c.mu.Unlock()

	log.Printf("JWKS refreshed: %d signing key(s)", len(keys))
	return nil
}

// getKey returns the RSA public key for the given kid.
// On a cache miss it re-fetches the JWKS once (handles key rotation).
func (c *jwksCache) getKey(kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	key, ok := c.keys[kid]
	c.mu.RUnlock()
	if ok {
		return key, nil
	}
	if err := c.refresh(); err != nil {
		return nil, err
	}
	c.mu.RLock()
	key, ok = c.keys[kid]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown kid: %s", kid)
	}
	return key, nil
}

func (c *jwksCache) keyFunc(t *jwt.Token) (interface{}, error) {
	kid, ok := t.Header["kid"].(string)
	if !ok {
		return nil, fmt.Errorf("missing kid in token header")
	}
	return c.getKey(kid)
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprint(w, body)
}

func helloHandler(cache *jwksCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, `{"error":"missing_token"}`)
			return
		}
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

		var claims jwt.MapClaims
		_, err := jwt.ParseWithClaims(tokenStr, &claims, cache.keyFunc,
			jwt.WithIssuer(issuer),
			jwt.WithExpirationRequired(),
			jwt.WithValidMethods([]string{"RS256"}),
		)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized,
				fmt.Sprintf(`{"error":"invalid_token","detail":%q}`, err.Error()))
			return
		}

		if !audContains(claims, audience) {
			writeJSON(w, http.StatusForbidden, `{"error":"insufficient_scope"}`)
			return
		}

		sub, _ := claims["sub"].(string)
		writeJSON(w, http.StatusOK,
			fmt.Sprintf(`{"message":"Hello from service-b!","sub":%q}`, sub))
	}
}

func audContains(claims jwt.MapClaims, want string) bool {
	switch v := claims["aud"].(type) {
	case string:
		return v == want
	case []interface{}:
		for _, a := range v {
			if s, ok := a.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

func main() {
	cache := &jwksCache{keys: make(map[string]*rsa.PublicKey)}
	if err := cache.refresh(); err != nil {
		log.Printf("WARN: initial JWKS fetch failed: %v (will retry on first request)", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/hello", helloHandler(cache))
	mux.HandleFunc("/healthz", healthzHandler)
	log.Printf("service-b listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
