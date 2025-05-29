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
all: check-and-test sync_pipenv lint_yamls kustomize-build

kustomize:
	curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"  | bash

.PHONY: check-and-test
check-and-test:
	$(CMD) -t rhtap -d rhobs/alerting/data_plane -y -p --tests-dir test/promql/tests/data_plane
	$(CMD) -t rhtap -d rhobs/recording -y -p --tests-dir test/promql/tests/recording

# Usage: make custom-check-files RULE_FILES="<rule_file1.yaml> <rule_file2.yaml>..." TEST_CASE_FILES="<test_file1.yaml> <test_file2.yaml>..."
.PHONY: custom-check-and-test
custom-check-and-test:
ifndef RULE_FILES
	$(error RULE_FILES is not set. Usage: make custom-check-files RULE_FILES="<file1.yaml>..." TEST_CASE_FILES="<test_file1.yaml>...")
endif
ifndef TEST_CASE_FILES
	$(error TEST_CASE_FILES is not set. Usage: make custom-check-files RULE_FILES="<file1.yaml>..." TEST_CASE_FILES="<test_file1.yaml>...")
endif
	@set -e; \
	TEMP_DIR=$$(mktemp -d "$(shell pwd)/selective_rules_check_temp.XXXXXX"); \
	TEMP_DIR_BASENAME=$$(basename $${TEMP_DIR}); \
	echo "Setting up temporary directories $$TEMP_DIR/rules/ and $$TEMP_DIR/tests/..."; \
	echo "$${TEMP_DIR}"; \
	echo "$${TEMP_DIR_BASENAME}"; \
	mkdir -p "$${TEMP_DIR}/rules/"; \
	mkdir -p "$${TEMP_DIR}/tests/"; \
	\
	echo "Copying rule files: $(RULE_FILES)"; \
	for rule_file in $(RULE_FILES); do \
		if [ -f "$${rule_file}" ]; then \
			cp "$${rule_file}" "$${TEMP_DIR}/rules/" || { echo "Error during rule file copy: $${rule_file}"; exit 1; }; \
		else \
			echo "Warning: Rule file $${rule_file} not found. Skipping." >&2; \
		fi; \
	done; \
	\
	echo "Copying test files: $(TEST_CASE_FILES)"; \
	for test_file in $(TEST_CASE_FILES); do \
		if [ -f "$${test_file}" ]; then \
			cp "$${test_file}" "$${TEMP_DIR}/tests/" || { echo "Error during test file copy: $${test_file}"; exit 1; }; \
		else \
			echo "Warning: Test file $${test_file} not found. Skipping." >&2; \
		fi; \
	done; \
	\
	echo "Running obsctl-reloader-rules-checker..."; \
	$(CMD) -t rhtap -d "/work/$${TEMP_DIR_BASENAME}/rules/" -y -p --tests-dir "/work/$${TEMP_DIR_BASENAME}/tests/"; \
	\
	echo "Cleaning up $${TEMP_DIR}/ directory..."; \
	rm -rf $${TEMP_DIR}; \
	\
	echo "Checker command processing finished."; \

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