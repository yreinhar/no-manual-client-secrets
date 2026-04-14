#!/usr/bin/env bash
set -euo pipefail

KC_POD=$(kubectl get pod -o name -n keycloak | grep keycloak | head -n1 | cut -d/ -f2)

kcadm() {
  kubectl exec "$KC_POD" -n keycloak -- /opt/keycloak/bin/kcadm.sh "$@"
}

echo "Login to Keycloak..." >&2
kcadm config credentials --server http://localhost:8080 --realm master --user admin --password admin

echo "Create realm..." >&2
kcadm create realms \
    -s realm="$KEYCLOAK_REALM" \
    -s enabled=true

echo "Create identity provider..." >&2
kcadm create identity-provider/instances \
    -r "$KEYCLOAK_REALM" \
    -s alias=kubernetes \
    -s providerId="$KEYCLOAK_PROVIDER" \
    -s config='{"issuer": "https://kubernetes.default.svc.cluster.local"}'

echo "Create client..." >&2
kcadm create clients \
    -r "$KEYCLOAK_REALM" \
    -s clientId="$KEYCLOAK_CLIENT" \
    -s serviceAccountsEnabled=true \
    -s clientAuthenticatorType=federated-jwt \
    -s attributes="{ \"jwt.credential.issuer\": \"kubernetes\", \"jwt.credential.sub\": \"system:serviceaccount:$APP_NAMESPACE:my-serviceaccount\" }"
