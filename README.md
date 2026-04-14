# no-manual-client-secrets

A demo showing how Kubernetes workloads can authenticate to Keycloak without managing static client secrets, using projected service account tokens and federated JWT client authentication.

## Overview

Managing static client secrets is operational overhead: they need to be created, rotated, stored in Kubernetes Secrets, and kept in sync. This demo eliminates that entirely.

**How it works:**

1. Kubernetes mints a short-lived JWT (projected service account token) scoped to the Keycloak realm as its audience (no human involvement needed).
2. Keycloak trusts Kubernetes as an OIDC identity provider (`https://kubernetes.default.svc.cluster.local`).
3. The Keycloak client is configured with `clientAuthenticatorType=federated-jwt` (there is no client secret to manage).
4. The workload presents its mounted token to Keycloak's token endpoint and receives a standard access token in return.
5. The workload uses that access token to call a downstream service, which validates it before responding.

## Services

```
┌─────────────────────────────────────────────────────────────────────┐
│ Kubernetes cluster                                                  │
│                                                                     │
│  namespace: service-a              namespace: service-b             │
│  ┌───────────────────────┐         ┌──────────────────────────┐     │
│  │ service-a (Go)        │         │ service-b (Go)           │     │
│  │                       │  JWT    │                          │     │
│  │ 1. read SA token      │────────▶│ 1. verify JWT signature  │     │
│  │    from mounted vol.  │         │    (JWKS from Keycloak)  │     │
│  │ 2. exchange for KC    │         │ 2. validate iss, aud,    │     │
│  │    access token       │         │    exp claims            │     │
│  │ 3. call service-b     │         │ 3. return 200/401/403    │     │
│  │    with Bearer token  │         │                          │     │
│  └──────────┬────────────┘         └──────────────────────────┘     │
│             │ token exchange                                        │
│             ▼                                                       │
│  ┌──────────────────────────────────────────┐                       │
│  │ Keycloak  (namespace: keycloak)          │                       │
│  │                                          │                       │
│  │ - realm: kubernetes                      │                       │
│  │ - identity provider: kubernetes (OIDC)   │                       │
│  │ - client: myclient (federated-jwt)       │                       │
│  │ - audience mapper: service-b             │                       │
│  └──────────────────────────────────────────┘                       │
└─────────────────────────────────────────────────────────────────────┘
```

| Service | Namespace | Role |
|---|---|---|
| service-a | `service-a` | Go client mounts a projected SA token, exchanges it for a Keycloak access token (cached, auto-refreshed), calls service-b |
| service-b | `service-b` | Go resource server validates the Bearer JWT on every request (signature via JWKS, issuer, audience `service-b`, expiry); no Keycloak session or secret needed |
| Keycloak | `keycloak` | Identity provider validates the SA token against the Kubernetes OIDC endpoint, issues a signed access token with `aud: service-b` |

**Token lifecycle:**

- The projected SA token has a 600s TTL. Kubelet rewrites the file before it expires. service-a reads it fresh on each Keycloak token exchange.
- The Keycloak access token is cached in service-a's memory and re-exchanged 30s before it expires, so downstream calls to service-b never block on a token refresh.
- service-b caches Keycloak's public keys (JWKS) in memory. On an unknown `kid` it re-fetches once, which transparently handles key rotation.

## Infrastructure

| Component | Role |
|---|---|
| Kind | Local Kubernetes cluster with ports 80/443 exposed |
| NGINX Ingress | Routes `keycloak.example.com` into the cluster |
| Keycloak (nightly) | Identity provider with `client-auth-federated` and `kubernetes-service-accounts` features enabled |

## Prerequisites

- [`kind`](https://kind.sigs.k8s.io/docs/user/quick-start/#installation), [`kubectl`](https://kubernetes.io/docs/tasks/tools/), [`docker`](https://docs.docker.com/get-docker/), `curl`, `jq` installed
- Ports 80 and 443 free on localhost
- Add the Keycloak hostname to your hosts file:

```sh
echo "127.0.0.1 keycloak.example.com" | sudo tee -a /etc/hosts
```

## Get Started

Run the steps below in order:

```sh
# 1. Spin up a Kind cluster with ports 80/443 exposed to the host
make kind-create-cluster

# 2. Deploy the NGINX ingress controller
make create-ingress-controller

# 3. Deploy the Keycloak StatefulSet and Ingress
make create-keycloak

# 4. Configure Keycloak: realm, Kubernetes identity provider, federated-JWT client, audience mapper
make setup-keycloak

# 5. Build service-a and service-b images and load them into the kind cluster
make build-service-a
make build-service-b

# 6. Deploy service-a (Go client pod with projected SA token)
make create-service-a

# 7. Deploy service-b (Go resource server)
make create-service-b
```

**Cleanup:**

```sh
make kind-delete-cluster
```
