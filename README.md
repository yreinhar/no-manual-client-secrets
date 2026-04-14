# no-manual-client-secrets

A demo showing how Kubernetes workloads can authenticate to Keycloak without managing static client secrets, using projected service account tokens and federated JWT client authentication.

## Overview

Managing static client secrets is operational overhead: they need to be created, rotated, stored in Kubernetes Secrets, and kept in sync. This demo eliminates that entirely.

**How it works:**

1. Kubernetes mints a short-lived JWT (projected service account token) scoped to the Keycloak realm as its audience (no human involvement needed).
2. Keycloak trusts Kubernetes as an OIDC identity provider (`https://kubernetes.default.svc.cluster.local`).
3. The Keycloak client is configured with `clientAuthenticatorType=federated-jwt` (there is no client secret to manage).
4. The workload presents its mounted token to Keycloak's token endpoint and receives a standard access token in return.

**Components:**

| Component | Role |
|---|---|
| Kind| Local Kubernetes cluster with ports 80/443 exposed |
| NGINX Ingress | Routes `keycloak.example.com` into the cluster |
| Keycloak (nightly) | Identity provider with `client-auth-federated` and `kubernetes-service-accounts` features enabled |
| Example pod | nginx pod with a projected service account token at `/var/run/secrets/tokens/kctoken` (600s TTL, audience-scoped to the Keycloak realm) |

## Prerequisites

- [`kind`](https://kind.sigs.k8s.io/docs/user/quick-start/#installation), [`kubectl`](https://kubernetes.io/docs/tasks/tools/), `curl`, `jq` installed
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

# 4. Configure Keycloak: create realm, Kubernetes identity provider, and federated-JWT client
make setup-keycloak

# 5. Deploy the example pod (service account + with projected token)
make create-pod

# 6. Exchange the pod's service account token for a Keycloak access token
make retrieve-access-token
```

A successful run prints a Keycloak access token to stdout. No secret was stored or configured anywhere.

**Cleanup:**

```sh
make kind-delete-cluster
```
