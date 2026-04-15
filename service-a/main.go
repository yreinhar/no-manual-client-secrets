package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
)

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

// tokenInfoHandler reads the projected SA token, decodes its JWT payload and
// returns the claims as JSON. No Keycloak call is made, useful for debugging
// the audience, issuer and subject before attempting a token exchange.
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
	writeJSON(w, http.StatusOK, map[string]any{
		"path":   saTokenPath,
		"length": len(tokenStr),
		"claims": claims,
	})
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

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Service-A Demo</title>
<style>
  body { font-family: monospace; max-width: 760px; margin: 40px auto; padding: 0 20px; background: #f9f9f9; color: #222; }
  h1 { font-size: 1.4rem; margin-bottom: 4px; }
  p.desc { color: #555; margin: 0 0 24px; font-size: 0.9rem; }
  .btn-row { display: flex; gap: 12px; flex-wrap: wrap; }
  button { padding: 10px 22px; font-size: 1rem; font-family: monospace; cursor: pointer; border: none; border-radius: 4px; }
  button:disabled { opacity: 0.5; cursor: default; }
  #btn-call  { background: #222; color: #fff; }
  #btn-token { background: #555; color: #fff; }
  #steps { margin-top: 24px; }
  .step { padding: 6px 0; font-size: 0.95rem; }
  .step.ok  { color: #1a7a1a; }
  .step.err { color: #b00; }
  .box { display: none; margin-top: 20px; }
  .box label { font-weight: bold; font-size: 0.9rem; display: block; margin-bottom: 4px; }
  pre { background: #eee; padding: 12px; border-radius: 4px; overflow-x: auto; font-size: 0.85rem; white-space: pre-wrap; word-break: break-all; margin: 0; }
  #error-msg { color: #b00; margin-top: 16px; font-size: 0.9rem; display: none; }
  hr { border: none; border-top: 1px solid #ddd; margin: 28px 0; }
</style>
</head>
<body>
<h1>Service-A</h1>
<p class="desc">
  Reads a Kubernetes projected SA token &rarr; exchanges it with Keycloak (no static secret) &rarr; calls service-b with the resulting Bearer token.
</p>

<div class="btn-row">
  <button id="btn-call" onclick="runCall()">Call Service-B</button>
  <button id="btn-token" onclick="inspectToken()">Inspect SA Token</button>
</div>

<div id="steps"></div>
<div id="response-box" class="box"><label>Response from service-b:</label><pre id="response-body"></pre></div>
<div id="token-box" class="box"><label>Projected SA token claims (decoded, not verified):</label><pre id="token-body"></pre></div>
<div id="error-msg"></div>

<script>
async function runCall() {
  const btn = document.getElementById('btn-call');
  const stepsEl = document.getElementById('steps');
  const respBox = document.getElementById('response-box');
  const respBody = document.getElementById('response-body');
  const tokenBox = document.getElementById('token-box');
  const errMsg = document.getElementById('error-msg');

  btn.disabled = true;
  stepsEl.innerHTML = '';
  respBox.style.display = 'none';
  tokenBox.style.display = 'none';
  errMsg.style.display = 'none';

  try {
    const res = await fetch('/call-service-b');
    const data = await res.json();

    (data.steps || []).forEach(s => {
      const d = document.createElement('div');
      d.className = 'step ' + (s.ok ? 'ok' : 'err');
      d.textContent = (s.ok ? '✓' : '✗') + '  ' + s.n + '. ' + s.name + ' — ' + s.detail;
      stepsEl.appendChild(d);
    });

    if (data.body) {
      let pretty = data.body;
      try { pretty = JSON.stringify(JSON.parse(data.body), null, 2); } catch(_) {}
      respBody.textContent = pretty;
      respBox.style.display = 'block';
    }
  } catch (e) {
    errMsg.textContent = 'Request failed: ' + e;
    errMsg.style.display = 'block';
  } finally {
    btn.disabled = false;
  }
}

async function inspectToken() {
  const btn = document.getElementById('btn-token');
  const tokenBox = document.getElementById('token-box');
  const tokenBody = document.getElementById('token-body');
  const stepsEl = document.getElementById('steps');
  const errMsg = document.getElementById('error-msg');

  btn.disabled = true;
  stepsEl.innerHTML = '';
  tokenBox.style.display = 'none';
  errMsg.style.display = 'none';

  try {
    const res = await fetch('/token-info');
    const data = await res.json();
    if (data.error) {
      errMsg.textContent = 'Error: ' + data.error;
      errMsg.style.display = 'block';
    } else {
      tokenBody.textContent = JSON.stringify(data, null, 2);
      tokenBox.style.display = 'block';
    }
  } catch (e) {
    errMsg.textContent = 'Request failed: ' + e;
    errMsg.style.display = 'block';
  } finally {
    btn.disabled = false;
  }
}
</script>
</body>
</html>`

func homeHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexHTML)
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", homeHandler)
	mux.HandleFunc("GET /call-service-b", callServiceBHandler)
	mux.HandleFunc("GET /token-info", tokenInfoHandler)
	mux.HandleFunc("GET /healthz", healthzHandler)
	log.Printf("service-a listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
