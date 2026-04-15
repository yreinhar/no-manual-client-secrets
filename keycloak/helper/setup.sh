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
    -s attributes="{ \"jwt.credential.issuer\": \"kubernetes\", \"jwt.credential.sub\": \"system:serviceaccount:$SERVICE_A_NAMESPACE:my-serviceaccount\", \"jwt.credential.audience\": \"http://keycloak.keycloak.svc.cluster.local:8080/realms/kubernetes\" }"

echo "Fetch client UUID..." >&2
CLIENT_UUID=$(kcadm get clients -r "$KEYCLOAK_REALM" \
    --fields id,clientId \
    | jq -r --arg cid "$KEYCLOAK_CLIENT" '.[] | select(.clientId==$cid) | .id')

echo "Create audience protocol mapper..." >&2
kcadm create clients/"$CLIENT_UUID"/protocol-mappers/models \
    -r "$KEYCLOAK_REALM" \
    -s name=service-b-audience \
    -s protocol=openid-connect \
    -s protocolMapper=oidc-audience-mapper \
    -s 'config={"included.custom.audience":"service-b","access.token.claim":"true","id.token.claim":"false","lightweight.claim":"false"}'
