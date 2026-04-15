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
SERVICE_A_NAMESPACE	 ?= service-a
SERVICE_B_NAMESPACE  ?= service-b


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
	@kubectl wait --namespace ingress-nginx \
		--for=condition=ready pod \
		--selector=app.kubernetes.io/component=controller \
		--timeout=90s

.PHONY: create-keycloak
create-keycloak: ## Create keycloak
	@echo Create Keycloak namespace... >&2
	@kubectl create ns $(KEYCLOAK_NAMESPACE)
	@echo Create Keycloak deployment... >&2
	@kubectl create -f $(GIT_ROOT)/keycloak/$(KEYCLOAK_DEPLOYMENT).yaml -n $(KEYCLOAK_NAMESPACE)
	@echo Create Keycloak ingress... >&2
	@kubectl create -f $(GIT_ROOT)/keycloak/$(KEYCLOAK_INGRESS).yaml -n $(KEYCLOAK_NAMESPACE)
	@kubectl wait --namespace $(KEYCLOAK_NAMESPACE) \
		--for=condition=ready pod \
		--selector=app=keycloak \
		--timeout=90s

.PHONY: setup-keycloak
setup-keycloak: ## Setup keycloak (realm, identitity provider, client)
	@KEYCLOAK_PROVIDER=$(KEYCLOAK_PROVIDER) \
	KEYCLOAK_REALM=$(KEYCLOAK_REALM) \
	KEYCLOAK_CLIENT=$(KEYCLOAK_CLIENT) \
	SERVICE_A_NAMESPACE=$(SERVICE_A_NAMESPACE) \
	bash $(GIT_ROOT)/keycloak/helper/setup.sh

.PHONY: build-service-a
build-service-a: ## Build service-a Docker image and load into kind cluster
	@echo Build service-a image... >&2
	@docker build -t service-a:latest $(GIT_ROOT)/service-a
	@echo Load image into kind cluster... >&2
	@kind load docker-image service-a:latest --name $(KIND_NAME)

.PHONY: build-service-b
build-service-b: ## Build service-b Docker image and load into kind cluster
	@echo Build service-b image... >&2
	@docker build -t service-b:latest $(GIT_ROOT)/service-b
	@echo Load image into kind cluster... >&2
	@kind load docker-image service-b:latest --name $(KIND_NAME)

.PHONY: create-service-a
create-service-a: ## Deploy service-a (Go client that exchanges SA token and calls service-b)
	@echo Create service-a namespace... >&2
	@kubectl create ns $(SERVICE_A_NAMESPACE)
	@echo Deploy service-a... >&2
	@kubectl create -f $(GIT_ROOT)/service-a/k8s/pod.yaml -n $(SERVICE_A_NAMESPACE)
	@kubectl wait --namespace $(SERVICE_A_NAMESPACE) \
		--for=condition=ready pod \
		--selector=app=service-a \
		--timeout=90s

.PHONY: create-service-b
create-service-b: ## Deploy service-b
	@echo Create service-b namespace... >&2
	@kubectl create ns $(SERVICE_B_NAMESPACE)
	@echo Deploy service-b... >&2
	@kubectl apply -f $(GIT_ROOT)/service-b/k8s/
	@kubectl wait --namespace $(SERVICE_B_NAMESPACE) \
		--for=condition=available deployment/service-b \
		--timeout=60s
