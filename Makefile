####################
# Defaults         #
####################

GIT_ROOT             := $(shell git rev-parse --show-toplevel)
KIND_IMAGE           ?= kindest/node:v1.35.1
KIND_NAME            ?= kind
KIND_CONFIG          ?= default
KEYCLOAK_DEPLOYMENT	 ?= keycloak
KEYCLOAK_INGRESS	 ?= ingress
KEYCLOAK_NAMESPACE	 ?= keycloak
KEYCLOAK_PROVIDER	 ?= kubernetes
KEYCLOAK_REALM		 ?= kubernetes
KEYCLOAK_CLIENT		 ?= myclient
KEYCLOAK_TOKEN_URL   ?= http://keycloak.example.com/realms/kubernetes/protocol/openid-connect/token
APP_NAMESPACE		 ?= service-a


####################
# Targets          #
####################

.PHONY: default
default: help

.PHONY: help 
help: ## Show help
	@sed -ne '/@sed/!s/## //p' $(MAKEFILE_LIST)

.PHONY: kind-create-cluster
kind-create-cluster: ## Create kind cluster
	@echo Create kind cluster... >&2
	@kind create cluster --name $(KIND_NAME) --image $(KIND_IMAGE) --config $(GIT_ROOT)/kind/$(KIND_CONFIG).yaml

.PHONY: kind-delete-cluster
kind-delete-cluster: ## Delete kind cluster
	@echo Delete kind cluster... >&2
	@kind delete cluster --name $(KIND_NAME)

.PHONY: create-ingress-controller
create-ingress-controller: ## Create ingress controller
	@echo Create ingress controller... >&2
	@kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml

.PHONY: create-keycloak
create-keycloak: ## Create keycloak
	@echo Create Keycloak namespace... >&2
	@kubectl create ns $(KEYCLOAK_NAMESPACE)
	@echo Create Keycloak deployment... >&2
	@kubectl create -f $(GIT_ROOT)/keycloak/$(KEYCLOAK_DEPLOYMENT).yaml -n $(KEYCLOAK_NAMESPACE)
	@echo Create Keycloak ingress... >&2
	@kubectl create -f $(GIT_ROOT)/keycloak/$(KEYCLOAK_INGRESS).yaml -n $(KEYCLOAK_NAMESPACE)

.PHONY: setup-keycloak
setup-keycloak: ## Setup keycloak (realm, identitity provider, client)
	@KEYCLOAK_PROVIDER=$(KEYCLOAK_PROVIDER) \
	KEYCLOAK_REALM=$(KEYCLOAK_REALM) \
	KEYCLOAK_CLIENT=$(KEYCLOAK_CLIENT) \
	APP_NAMESPACE=$(APP_NAMESPACE) \
	bash $(GIT_ROOT)/keycloak/helper/setup.sh

.PHONY: create-pod
create-pod: ## Create example pod
	@echo Create app namespace... >&2
	@kubectl create ns $(APP_NAMESPACE)
	@echo Creating pod... >&2
	@kubectl create -f $(GIT_ROOT)/app/pod.yaml -n $(APP_NAMESPACE)

.PHONY: retrieve-access-token
retrieve-access-token: ## Retrieve access token from keycloak
	@echo Retrieve access token... >&2
	@TOKEN=$$(kubectl exec my-pod -- cat /var/run/secrets/tokens/kctoken); \
	curl --insecure -X POST \
		-d grant_type=client_credentials \
		-d client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer \
		-d client_assertion="$$TOKEN" \
		$(KEYCLOAK_TOKEN_URL) | jq
		
