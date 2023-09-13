SHELL=/usr/bin/env bash -o pipefail

VPATH ?= $(shell pwd):$(PATH)
export PATH := $(VPATH)
# Runtime CLI to use for running images
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)
IMG_VERSION=1.0.4

BASE_CMD=$(CONTAINER_ENGINE) run -v "$(shell pwd):/work" --rm --privileged -t quay.io/rhobs/obsctl-reloader-rules-checker:$(IMG_VERSION) -t rhtap
 
ifeq ($(CMD),)
CMD := ${BASE_CMD}
endif

.PHONY: all
all: check-and-test

.PHONY: check-and-test
check-and-test:
	$(CMD) -t rhtap -d rhobs/alerting/data_plane -y -p --tests-dir test/promql/tests/data_plane
	$(CMD) -t rhtap -d rhobs/alerting/control_plane -y -p --tests-dir test/promql/tests/control_plane
