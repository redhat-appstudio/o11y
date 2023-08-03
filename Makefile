SHELL=/usr/bin/env bash -o pipefail

VPATH ?= $(shell pwd):$(PATH)
export PATH := $(VPATH)
# Runtime CLI to use for running images
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)
IMG_VERSION=1.0.4

BASE_CMD=$(CONTAINER_ENGINE) run -v "$(shell pwd):/work" --rm --privileged -t quay.io/rhobs/obsctl-reloader-rules-checker:$(IMG_VERSION) -t rhtap -d rhobs/alerting -y -p

.PHONY: all
all: check-and-test kustomize-build

.PHONY: check-and-test
check-and-test:
	$(BASE_CMD) --tests-dir test/promql/tests

.PHONY: check
check:
	$(BASE_CMD)

kustomize:
	curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"  | bash

.PHONY: kustomize-build
kustomize-build: kustomize
	# This validates that the build command passes and not its output's validity.
	# It will fail once we have more than one subdirectory, which will prevent us from
	# adding untested configurations (this target will have to chage when that happens).
	kustomize build prometheus/* 1>/dev/null
