package main

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
)

//go:embed static
var staticFiles embed.FS

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var (
	saTokenPath = getEnv("SA_TOKEN_PATH", "/var/run/secrets/tokens/kctoken")
	kcTokenURL  = getEnv("KC_TOKEN_URL", "http://keycloak.keycloak.svc.cluster.local:8080/realms/kubernetes/protocol/openid-connect/token")
	serviceBURL = getEnv("SERVICE_B_URL", "http://service-b.service-b.svc.cluster.local/hello")
	addr        = getEnv("ADDR", ":8080")
)

// decodeJWTClaims base64url-decodes the payload section of a JWT without
// verifying the signature. Used only for debugging/display.
func decodeJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("not a JWT (expected 3 parts, got %d)", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("base64 decode payload: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}
	return claims, nil
}

type Step struct {
	N      int    `json:"n"`
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type callResult struct {
	Steps []Step `json:"steps"`
	Body  string `json:"body,omitempty"`
}

type kcTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// tokenInfoHandler reads the projected SA token, decodes its JWT payload,
// exchanges it with Keycloak, and returns the SA token claims together with
// the resulting access token and its decoded claims.
func tokenInfoHandler(w http.ResponseWriter, _ *http.Request) {
	raw, err := os.ReadFile(saTokenPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("read %s: %v", saTokenPath, err),
		})
		return
	}
	tokenStr := strings.TrimSpace(string(raw))
	log.Printf("[token-info] SA token read: %d bytes", len(tokenStr))

	claims, err := decodeJWTClaims(tokenStr)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("decode JWT: %v", err),
		})
		return
	}
	log.Printf("[token-info] decoded claims: iss=%v aud=%v sub=%v", claims["iss"], claims["aud"], claims["sub"])

	result := map[string]any{
		"path":   saTokenPath,
		"length": len(tokenStr),
		"claims": claims,
	}

	// Exchange SA token for a Keycloak access token.
	resp, err := http.PostForm(kcTokenURL, url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {tokenStr},
	})
	if err != nil {
		result["kc_error"] = fmt.Sprintf("token exchange request: %v", err)
		log.Printf("[token-info] Keycloak exchange ERROR: %v", err)
		writeJSON(w, http.StatusOK, result)
		return
	}
	defer resp.Body.Close()

	var tr kcTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		result["kc_error"] = fmt.Sprintf("decode Keycloak response (HTTP %d): %v", resp.StatusCode, err)
		log.Printf("[token-info] Keycloak decode ERROR: %v", err)
		writeJSON(w, http.StatusOK, result)
		return
	}
	if tr.AccessToken == "" {
		result["kc_error"] = fmt.Sprintf("no access_token in response (HTTP %d): %s — %s", resp.StatusCode, tr.Error, tr.ErrorDesc)
		log.Printf("[token-info] Keycloak ERROR: %s", result["kc_error"])
		writeJSON(w, http.StatusOK, result)
		return
	}
	log.Printf("[token-info] access token received, expires_in=%ds", tr.ExpiresIn)
	result["access_token"] = tr.AccessToken
	result["access_token_expires_in"] = tr.ExpiresIn

	atClaims, err := decodeJWTClaims(tr.AccessToken)
	if err != nil {
		result["kc_error"] = fmt.Sprintf("decode access token claims: %v", err)
	} else {
		result["access_token_claims"] = atClaims
	}

	writeJSON(w, http.StatusOK, result)
}

func callServiceBHandler(w http.ResponseWriter, r *http.Request) {
	result := callResult{}

	// ── Step 1: read projected SA token ──────────────────────────────────
	log.Printf("[step 1] reading SA token from %s", saTokenPath)
	saToken, err := os.ReadFile(saTokenPath)
	if err != nil {
		detail := fmt.Sprintf("read %s: %v", saTokenPath, err)
		log.Printf("[step 1] ERROR — %s", detail)
		result.Steps = append(result.Steps, Step{N: 1, Name: "Read projected SA token", OK: false, Detail: detail})
		writeJSON(w, http.StatusInternalServerError, result)
		return
	}
	tokenStr := strings.TrimSpace(string(saToken))
	step1Detail := fmt.Sprintf("%d bytes from %s", len(tokenStr), saTokenPath)
	log.Printf("[step 1] OK — token length: %d bytes (not empty)", len(tokenStr))
	result.Steps = append(result.Steps, Step{N: 1, Name: "Read projected SA token", OK: true, Detail: step1Detail})

	// ── Step 2: exchange SA token for Keycloak access token ───────────────
	log.Printf("[step 2] exchanging SA token with Keycloak: %s", kcTokenURL)
	resp, err := http.PostForm(kcTokenURL, url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {tokenStr},
	})
	if err != nil {
		detail := fmt.Sprintf("token exchange request: %v", err)
		log.Printf("[step 2] ERROR — %s", detail)
		result.Steps = append(result.Steps, Step{N: 2, Name: "Exchange with Keycloak", OK: false, Detail: detail})
		writeJSON(w, http.StatusInternalServerError, result)
		return
	}
	defer resp.Body.Close()

	var tr kcTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		detail := fmt.Sprintf("decode Keycloak response (HTTP %d): %v", resp.StatusCode, err)
		log.Printf("[step 2] ERROR — %s", detail)
		result.Steps = append(result.Steps, Step{N: 2, Name: "Exchange with Keycloak", OK: false, Detail: detail})
		writeJSON(w, http.StatusInternalServerError, result)
		return
	}
	if tr.AccessToken == "" {
		detail := fmt.Sprintf("no access_token in response (HTTP %d): %s — %s", resp.StatusCode, tr.Error, tr.ErrorDesc)
		log.Printf("[step 2] ERROR — %s", detail)
		result.Steps = append(result.Steps, Step{N: 2, Name: "Exchange with Keycloak", OK: false, Detail: detail})
		writeJSON(w, http.StatusInternalServerError, result)
		return
	}
	step2Detail := fmt.Sprintf("access token received, expires_in=%ds, token length: %d bytes", tr.ExpiresIn, len(tr.AccessToken))
	log.Printf("[step 2] OK — %s", step2Detail)
	result.Steps = append(result.Steps, Step{N: 2, Name: "Exchange with Keycloak", OK: true, Detail: step2Detail})

	// ── Step 3: call service-b with Bearer token ──────────────────────────
	log.Printf("[step 3] calling service-b: %s", serviceBURL)
	req, _ := http.NewRequest(http.MethodGet, serviceBURL, nil)
	req.Header.Set("Authorization", "Bearer "+tr.AccessToken)

	bResp, err := http.DefaultClient.Do(req)
	if err != nil {
		detail := fmt.Sprintf("call service-b: %v", err)
		log.Printf("[step 3] ERROR — %s", detail)
		result.Steps = append(result.Steps, Step{N: 3, Name: "Call service-b", OK: false, Detail: detail})
		writeJSON(w, http.StatusBadGateway, result)
		return
	}
	defer bResp.Body.Close()

	body, _ := io.ReadAll(bResp.Body)
	step3Detail := fmt.Sprintf("HTTP %d", bResp.StatusCode)
	log.Printf("[step 3] OK — %s", step3Detail)
	result.Steps = append(result.Steps, Step{N: 3, Name: "Call service-b", OK: bResp.StatusCode < 400, Detail: step3Detail})
	result.Body = strings.TrimSpace(string(body))

	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

func main() {
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("embed sub: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticFS)))
	mux.HandleFunc("GET /call-service-b", callServiceBHandler)
	mux.HandleFunc("GET /token-info", tokenInfoHandler)
	mux.HandleFunc("GET /healthz", healthzHandler)
	log.Printf("service-a listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
