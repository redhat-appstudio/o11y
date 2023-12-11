SHELL=/usr/bin/env bash -o pipefail

VPATH ?= $(shell pwd):$(PATH)
export PATH := $(VPATH)
# Runtime CLI to use for running images
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)
IMG_VERSION=1.0.4
EXPORTERS_IMG ?= quay.io/redhat-appstudio/o11y-prometheus-exporters:latest 

BASE_CMD=$(CONTAINER_ENGINE) run -v "$(shell pwd):/work" --rm --privileged -t quay.io/rhobs/obsctl-reloader-rules-checker:$(IMG_VERSION) -t rhtap
 
ifeq ($(CMD),)
CMD := ${BASE_CMD}
endif

.PHONY: all
all: check-and-test sync_pipenv lint_yamls kustomize-build

kustomize:
	curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"  | bash

.PHONY: check-and-test
check-and-test:
	$(CMD) -t rhtap -d rhobs/alerting/data_plane -y -p --tests-dir test/promql/tests/data_plane
	$(CMD) -t rhtap -d rhobs/alerting/control_plane -y -p --tests-dir test/promql/tests/control_plane

.PHONY: install_pipenv
install_pipenv:
	python3 -m pip install pipenv

.PHONY: sync_pipenv
sync_pipenv:
	python3 -m pipenv sync --dev

.PHONY: lint_yamls
lint_yamls:
	python3 -m pipenv run yamllint . && echo "lint_yamls: SUCCESS"

.PHONY: kustomize-build
kustomize-build: kustomize
	# This validates that the build command passes and not its output's validity.
	kustomize build config/probes/monitoring/grafana/base 1>/dev/null

.PHONY: install_docker
install_docker:
	yum install -y $(CONTAINER_ENGINE)
	yum install -y uidmap

.PHONY: docker_build
docker_build:
	$(CONTAINER_ENGINE) build --no-cache --userns=keep-id -t ${EXPORTERS_IMG} .

.PHONY: docker_push
docker_push:
	$(CONTAINER_ENGINE) push ${EXPORTERS_IMG}
