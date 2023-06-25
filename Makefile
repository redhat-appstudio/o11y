PINT_VERSION = 0.42.2
PROMTOOL_VERSION = 2.42.0
YQ_VERSION = 4.31.2
EXTRACTED_RULE_FILE = test/promql/extracted-rules.yaml
ALERTING_RULE_FILES = rhobs/alerting/*.yaml
GRAFANA_DASHBOARDS = $(shell ls grafana/dashboards) 
GOPATH = $(shell go env GOPATH)

.PHONY: all
all: prepare sync_pipenv lint test_rules pint_lint lint_yamls dashboard_linter lint_grafana_dashboards kustomize_build

.PHONY: prepare
prepare: pint promtool yq kustomize
	echo "Extract Prometheus rules"
	(./yq eval-all '. as $$item ireduce ({}; . *+ $$item)' ${ALERTING_RULE_FILES} | ./yq ".spec" ) > ${EXTRACTED_RULE_FILE}

.PHONY: lint
lint:
	echo "Running Prometheus rules linter tests"
	./promtool check rules ${EXTRACTED_RULE_FILE}

.PHONY: test_rules
test_rules:
	echo "Running Prometheus rules unit tests"
	./promtool test rules test/promql/tests/*

pint:
	echo 'Installing pint...'
	curl -OJL 'https://github.com/cloudflare/pint/releases/download/v${PINT_VERSION}/pint-${PINT_VERSION}-linux-amd64.tar.gz'
	tar xvzf 'pint-${PINT_VERSION}-linux-amd64.tar.gz' pint-linux-amd64
	mv pint-linux-amd64 pint
	rm 'pint-${PINT_VERSION}-linux-amd64.tar.gz'	

promtool:
	echo "Installing promtool..."
	curl -OJL "https://github.com/prometheus/prometheus/releases/download/v${PROMTOOL_VERSION}/prometheus-${PROMTOOL_VERSION}.linux-amd64.tar.gz"
	tar xvzf "prometheus-${PROMTOOL_VERSION}.linux-amd64.tar.gz" "prometheus-${PROMTOOL_VERSION}.linux-amd64"/promtool --strip-components=1
	rm -f prometheus-${PROMTOOL_VERSION}.linux-amd64.tar.gz

yq:
	echo "Installing yq..."
	curl -OJL "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_linux_amd64.tar.gz"
	tar xvzf yq_linux_amd64.tar.gz ./yq_linux_amd64
	rm -f yq_linux_amd64.tar.gz
	mv yq_linux_amd64 yq

kustomize:
	curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"  | bash

dashboard_linter:
	go install github.com/grafana/dashboard-linter@v0.0.0-20230531105903-cd7fcaf3bec8

.PHONY: pint_lint
pint_lint:
	echo "Linting Prometheus rules..."
	./pint --no-color lint ${EXTRACTED_RULE_FILE}

.PHONY: install_pipenv
install_pipenv:
	python3 -m pip install pipenv

.PHONY: sync_pipenv
sync_pipenv:
	python3 -m pipenv sync --dev

.PHONY: lint_yamls
lint_yamls:
	python3 -m pipenv run yamllint . && echo "lint_yamls: SUCCESS"

.PHONY: lint_grafana_dashboards
lint_grafana_dashboards:
	@for dashboard in ${GRAFANA_DASHBOARDS} ; do \
		echo -e "Linting dashboard $$dashboard\n"; \
		${GOPATH}/bin/dashboard-linter lint grafana/dashboards/$$dashboard; done
	@echo

.PHONY: kustomize_build
kustomize_build:
	# This validates that the build command passes and not its output's validity.
	# It will fail once we have more than one subdirectory, which will prevent us from
	# adding untested configurations (this target will have to chage when that happens).
	./kustomize build prometheus/* 1>/dev/null
