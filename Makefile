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
all: check-and-test check-alert-conventions check-env-drift sync_pipenv lint_yamls kustomize-build

kustomize:
	curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"  | bash

.PHONY: check-and-test
check-and-test:
	$(CMD) -t rhtap -d rhobs/alerting/data_plane -y -p --tests-dir test/promql/tests/data_plane
	$(CMD) -t rhtap -d rhobs/alerting/konflux-release-data -y -p --tests-dir test/promql/tests/konflux-release-data
	$(CMD) -t rhtap -d rhobs/recording -y -p --tests-dir test/promql/tests/recording
	$(CMD) -t rhtap -d rhobs/staging/alerting/data-plane/alerts -y -p --tests-dir rhobs/staging/alerting/data-plane/tests
	$(CMD) -t rhtap -d rhobs/staging/alerting/konflux-release-data/alerts -y -p --tests-dir rhobs/staging/alerting/konflux-release-data/tests
	$(CMD) -t rhtap -d rhobs/staging/recording/rules -y -p --tests-dir rhobs/staging/recording/tests

.PHONY: check-env-drift
check-env-drift:
	@if [ -d rhobs/staging ] && [ -d rhobs/production ]; then \
		python3 scripts/check-env-drift.py; \
	else \
		echo "check-env-drift: skipped (waiting for both rhobs/staging/ and rhobs/production/)"; \
	fi

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