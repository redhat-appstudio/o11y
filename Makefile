SHELL=/usr/bin/env bash -o pipefail

VPATH ?= $(shell pwd):$(PATH)
export PATH := $(VPATH)
# Runtime CLI to use for running images
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || echo docker)
IMG_VERSION=1.0.4

BASE_CMD=$(CONTAINER_ENGINE) run -v "$(shell pwd):/work" --rm --privileged -t quay.io/rhobs/obsctl-reloader-rules-checker:$(IMG_VERSION) -t rhtap
CHECK_SCRIPT := ./scripts/selective-check-and-test.sh

ifeq ($(CMD),)
CMD := ${BASE_CMD}
endif

.PHONY: all
all: check-and-test check-alert-conventions sync_pipenv lint_yamls kustomize-build

KUSTOMIZE_VERSION ?= v5.6.0

kustomize:
	@if command -v kustomize >/dev/null 2>&1; then \
		echo "kustomize already installed: $$(kustomize version)"; \
	else \
		OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
		ARCH=$$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/'); \
		TARBALL="kustomize_$(KUSTOMIZE_VERSION)_$${OS}_$${ARCH}.tar.gz"; \
		BASE_URL="https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2F$(KUSTOMIZE_VERSION)"; \
		echo "Downloading kustomize $(KUSTOMIZE_VERSION) ($${OS}/$${ARCH})..."; \
		download_ok=false; \
		for i in 1 2 3; do \
			if curl -fSL --retry 3 --retry-delay 5 \
				"$${BASE_URL}/$${TARBALL}" \
				-o kustomize.tar.gz && \
			   curl -fSL --retry 3 --retry-delay 5 \
				"$${BASE_URL}/kustomize_$(KUSTOMIZE_VERSION)_checksums.txt" \
				-o kustomize_checksums.txt; then \
				download_ok=true; \
				break; \
			fi; \
			echo "Download attempt $$i failed, retrying in 5s..."; \
			sleep 5; \
		done; \
		if [ "$$download_ok" != "true" ]; then \
			echo "ERROR: Failed to download kustomize after 3 attempts"; \
			rm -f kustomize.tar.gz kustomize_checksums.txt; \
			exit 1; \
		fi; \
		echo "$$(grep "$${TARBALL}" kustomize_checksums.txt)" | sha256sum -c - || \
			{ echo "ERROR: checksum verification failed"; rm -f kustomize.tar.gz kustomize_checksums.txt; exit 1; }; \
		tar xzf kustomize.tar.gz && \
		rm -f kustomize.tar.gz kustomize_checksums.txt && \
		[ -x ./kustomize ] || { echo "ERROR: extraction failed"; rm -f kustomize.tar.gz kustomize_checksums.txt; exit 1; }; \
		echo "kustomize $(KUSTOMIZE_VERSION) installed successfully"; \
	fi

.PHONY: check-and-test
check-and-test:
	$(CMD) -t rhtap -d rhobs/alerting/data_plane -y -p --tests-dir test/promql/tests/data_plane
	$(CMD) -t rhtap -d rhobs/recording -y -p --tests-dir test/promql/tests/recording

.PHONY: check-alert-conventions
check-alert-conventions:
	$(CONTAINER_ENGINE) run -v "$(shell pwd):/work" --rm --entrypoint python3 -t quay.io/rhobs/obsctl-reloader-rules-checker:$(IMG_VERSION) /work/scripts/check-alert-conventions.py

.PHONY: selective-check-and-test
selective-check-and-test:
    ifndef RULE_FILES
	$(error RULE_FILES is not set. Usage: make selective-check-and-test RULE_FILES="<file1.yaml>..." TEST_CASE_FILES="<test_file1.yaml>...")
    endif
    ifndef TEST_CASE_FILES
	$(error TEST_CASE_FILES is not set. Usage: make selective-check-and-test RULE_FILES="<file1.yaml>..." TEST_CASE_FILES="<test_file1.yaml>...")
    endif
	@echo "Executing selective-check-and-test..."
	@$(CHECK_SCRIPT) "$(CMD)" "$(RULE_FILES)" "$(TEST_CASE_FILES)"
	@echo "Execution completed."

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