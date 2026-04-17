# no-manual-client-secrets

A demo showing how Kubernetes workloads can authenticate to Keycloak **without managing static client secrets**, using projected service account tokens and federated JWT client authentication.

> **Not production-ready.**
> This is an educational demo running on a local Kind cluster with `start-dev` Keycloak, plain HTTP, no TLS, and a bootstrapped admin password. See [Potential Improvements](#potential-improvements) for what would need to change before running anything like outside a demo.

---

## The Problem

Managing static client secrets is operational overhead: they must be created, rotated, stored in Kubernetes Secrets, and kept in sync across environments. A leaked or stale secret means a security incident or a broken deployment.

This demo eliminates the secret entirely. The workload authenticates using its own Kubernetes identity, a projected service account token, and Keycloak trusts Kubernetes as an OIDC identity provider.

---

## How It Works

```
service-a pod                               service-b pod
┌────────────────────────────┐              ┌──────────────────────────────┐
│                            │              │                              │
│  1. Read SA token          │              │  1. Verify JWT signature     │
│     /var/run/secrets/      │              │     (JWKS from Keycloak)     │
│     tokens/kctoken         │              │                              │
│     (600s TTL, auto-       │              │  2. Validate iss, aud, exp   │
│      rotated by Kubelet)   │              │                              │
│                            │              │  3. Return 200 / 401 / 403   │
│  2. Exchange SA token      │─── Bearer ──▶│                              │
│     with Keycloak          │    token     └──────────────────────────────┘
│     (client_credentials    │
│      + jwt-bearer          │
│      assertion)            │
│                            │
│  3. Call service-b with    │
│     the access token       │
└────────────┬───────────────┘
             │ token exchange
             ▼
┌────────────────────────────────────────────┐
│ Keycloak  (namespace: keycloak)            │
│                                            │
│  - realm: kubernetes                       │
│  - identity provider: kubernetes (OIDC)    │
│    issuer: https://kubernetes.default      │
│            .svc.cluster.local              │
│  - client: myclient                        │
│    clientAuthenticatorType: federated-jwt  │
│    (no client secret configured)           │
│  - audience mapper → aud: service-b        │
└────────────────────────────────────────────┘
```

Step-by-step:

1. Kubernetes mints a short-lived JWT (the projected SA token) with `aud: http://keycloak.keycloak.svc.cluster.local:8080/realms/kubernetes` and mounts it into the service-a pod. No human creates or stores this token.
2. service-a reads the token and posts it to Keycloak's token endpoint as a `client_assertion` (RFC 7523 JWT bearer flow).
3. Keycloak validates the SA token signature against the Kubernetes OIDC JWKS endpoint (`https://kubernetes.default.svc.cluster.local`), checks the `iss`, `sub`, and `aud` claims against the client configuration, and returns a signed access token with `aud: service-b`.
4. service-a forwards that access token as a Bearer header to service-b.
5. service-b verifies the signature (JWKS fetched from Keycloak), the issuer, the `service-b` audience, and the expiry — then returns a 200.

---

## Services

| Service | Namespace | Role |
|---|---|---|
| service-a | `service-a` | Go HTTP server with a UI. On each request it reads the projected SA token, exchanges it with Keycloak for an access token, and calls service-b. |
| service-b | `service-b` | Go resource server. Validates the Bearer JWT on every request (signature via JWKS cache, issuer, audience `service-b`, expiry). No Keycloak session or secret needed. |
| Keycloak | `keycloak` | Identity provider. Validates the SA token against Kubernetes OIDC and issues a signed access token scoped to `service-b`. |

---

## Infrastructure

| Component | Role |
|---|---|
| Kind | Local Kubernetes cluster |
| Keycloak nightly | Preview features `client-auth-federated` and `kubernetes-service-accounts` enabled |

---

## Prerequisites

- [`kind`](https://kind.sigs.k8s.io/docs/user/quick-start/#installation), [`kubectl`](https://kubernetes.io/docs/tasks/tools/), [`docker`](https://docs.docker.com/get-docker/), `jq` installed

---

## Setup

Run the steps below in order. Each `make` target waits for its resources to become ready before returning.

```sh
# 1. Local Kubernetes cluster (Kind)
make kind-create-cluster


# 2. Keycloak StatefulSet
make create-keycloak

# 3. Configure Keycloak: realm, Kubernetes OIDC identity provider,
#    federated-JWT client (myclient), and the service-b audience mapper
make setup-keycloak

# 4. Build Go images and load them into the Kind cluster
make build-service-a
make build-service-b

# 5. Deploy service-a (pod with projected SA token volume)
make create-service-a

# 6. Deploy service-b (Deployment + ClusterIP Service)
make create-service-b
```

**Tear down:**

```sh
make kind-delete-cluster
```

---

## Testing

service-a is a plain pod with no Ingress. Use `kubectl port-forward` to reach its UI:

```sh
kubectl port-forward pod/service-a 8080:8080 -n service-a
```

Open **http://localhost:8080/** in your browser. 

```sh
open http://localhost:8080/
```

The UI has two buttons:

### Inspect SA Token

Decodes the projected SA token's claims **and** exchanges it with Keycloak to display the resulting access token. Useful to inspect the full credential chain in one place:

**SA token claims** — shown first, without a Keycloak call:
- `iss` → `https://kubernetes.default.svc.cluster.local`
- `sub` → `system:serviceaccount:service-a:my-serviceaccount`
- `aud` → `http://keycloak.keycloak.svc.cluster.local:8080/realms/kubernetes`
- `exp` → token expiry (within 600s of issuance)

**Keycloak access token** — shown below the SA token claims:
- Raw token string (the JWT as returned by Keycloak)
- Decoded access token claims (e.g. `iss`, `sub`, `aud: service-b`, `exp`)

If the Keycloak exchange fails (e.g. Keycloak is not yet ready), the SA token claims are still displayed and a `kc_error` message is shown instead of the access token section.

### Call Service-B

Runs the full three-step flow and shows each step's result inline:

```
✓ 1. Read projected SA token   — 1196 bytes from /var/run/secrets/tokens/kctoken
✓ 2. Exchange with Keycloak    — access token received, expires_in=300s
✓ 3. Call service-b            — HTTP 200
```

Response from service-b:
```json
{
  "message": "Hello from service-b!",
  "sub": "..."
}
```

If step 2 fails, check the Keycloak logs and verify `make setup-keycloak` completed successfully. If step 3 fails with `invalid_token: token has invalid issuer`, service-b needs to be restarted after a config change:

```sh
kubectl rollout restart deployment/service-b -n service-b
```

### Logs

```sh
# service-a shows SA token read, Keycloak exchange, service-b call
kubectl logs pod/service-a -n service-a -f

# service-b shows JWT validation results per request
kubectl logs deployment/service-b -n service-b -f

# Keycloak useful for debugging federated-JWT rejections
kubectl logs statefulset/keycloak -n keycloak -f
```

---

## Key Configuration

### Projected SA token (`service-a/k8s/pod.yaml`)

```yaml
volumes:
- name: vault-token
  projected:
    sources:
    - serviceAccountToken:
        path: kctoken
        expirationSeconds: 600
        audience: http://keycloak.keycloak.svc.cluster.local:8080/realms/kubernetes
```

The `audience` must match Keycloak's realm URL (its OIDC issuer). Using the full token-endpoint path (`/protocol/openid-connect/token`) causes Keycloak to reject the assertion.

### Keycloak client (`keycloak/helper/setup.sh`)

```bash
clientAuthenticatorType=federated-jwt
jwt.credential.issuer   = kubernetes          # IdP alias
jwt.credential.sub      = system:serviceaccount:service-a:my-serviceaccount
jwt.credential.audience = http://keycloak.keycloak.svc.cluster.local:8080/realms/kubernetes
```

No `clientSecret` is ever set. Keycloak locates the client by matching `sub` and validates the signature via the Kubernetes OIDC JWKS endpoint.

---

> 💡 Found this useful? I write about Kubernetes, cloud-native and platform engineering at [opinionatedops.substack.com](https://opinionatedops.substack.com/). Would love to have you there.

---

## Potential Improvements

### Token caching in service-a

Currently service-a reads the SA token from disk and calls Keycloak on **every** incoming request. In practice you would cache the Keycloak access token in memory and only re-exchange it when it is about to expire (e.g. 30 seconds before the `expires_in` deadline). The SA token itself does not need to be cached — Kubelet rotates the file automatically before it expires, so reading it fresh is cheap and always correct.

Rough sketch:

```go
type tokenCache struct {
    mu      sync.Mutex
    token   string
    expiry  time.Time
}

func (c *tokenCache) get() (string, bool) {
    c.mu.Lock()
    defer c.mu.Unlock()
    return c.token, time.Now().Before(c.expiry.Add(-30*time.Second))
}
```

### TLS everywhere

All traffic in this demo is plain HTTP. In production:
- Keycloak should be fronted by TLS (cert-manager + Let's Encrypt or an internal CA).
- Service-to-service calls should use mTLS (e.g. via a service mesh like Istio or Linkerd).

### Stable Keycloak hostname

Without `KC_HOSTNAME` set, Keycloak derives its issuer URL from the incoming request's `Host` header. Tokens issued via the ingress URL and tokens issued via the internal service URL have different `iss` claims. Set a single stable hostname:

```yaml
- name: KC_HOSTNAME
  value: "keycloak.example.com"
- name: KC_HOSTNAME_STRICT
  value: "true"
```

Then both service-a's SA token audience and service-b's `ISSUER` env var can use the same external URL, and a DNS entry inside the cluster (CoreDNS rewrite or an internal Service) routes it to Keycloak.

### Production Keycloak

Replace `start-dev` with a production-mode deployment:
- Use a persistent database (PostgreSQL)
- Remove the bootstrap admin environment variables; use `kcadm` or the Admin REST API with a dedicated automation credential.
- Enable Keycloak clustering if you need HA.

### RBAC hardening

The service-a ServiceAccount is currently granted no explicit RBAC rules (it relies only on the default). In a real environment you would apply the principle of least privilege and ensure the SA token is usable only from the intended pod.
