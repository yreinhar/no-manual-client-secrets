package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var (
	saTokenPath = getEnv("SA_TOKEN_PATH", "/var/run/secrets/tokens/kctoken")
	kcTokenURL  = getEnv("KC_TOKEN_URL", "http://keycloak.keycloak.svc.cluster.local/realms/kubernetes/protocol/openid-connect/token")
	serviceBURL = getEnv("SERVICE_B_URL", "http://service-b.service-b.svc.cluster.local/hello")
	addr        = getEnv("ADDR", ":8080")
)

// avoid a remote call on every incoming request to the keycloak token endpoint
type tokenCache struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

type kcTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error"`
}

// get returns the cached Keycloak access token, refreshing if expired or missing.
// On refresh it reads the projected SA token fresh from disk — kubelet keeps
// that file current, so no file watcher is needed.
func (c *tokenCache) get() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	const buffer = 30 * time.Second
	if c.token != "" && time.Now().Before(c.expiresAt.Add(-buffer)) {
		return c.token, nil
	}

	saToken, err := os.ReadFile(saTokenPath)
	if err != nil {
		return "", fmt.Errorf("read SA token from %s: %w", saTokenPath, err)
	}

	resp, err := http.PostForm(kcTokenURL, url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {strings.TrimSpace(string(saToken))},
	})
	if err != nil {
		return "", fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	var tr kcTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("token exchange failed: %s", tr.Error)
	}

	ttl := time.Duration(tr.ExpiresIn) * time.Second
	c.token = tr.AccessToken
	c.expiresAt = time.Now().Add(ttl)
	log.Printf("KC token refreshed, valid for %v", ttl)
	return c.token, nil
}

func callServiceBHandler(cache *tokenCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, err := cache.get()
		if err != nil {
			log.Printf("ERROR: get token: %v", err)
			http.Error(w, `{"error":"token_error"}`, http.StatusInternalServerError)
			return
		}

		req, _ := http.NewRequest(http.MethodGet, serviceBURL, nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("ERROR: call service-b: %v", err)
			http.Error(w, `{"error":"upstream_error"}`, http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

func main() {
	cache := &tokenCache{}
	mux := http.NewServeMux()
	mux.HandleFunc("/call-service-b", callServiceBHandler(cache))
	mux.HandleFunc("/healthz", healthzHandler)
	log.Printf("service-a listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
