PINT_VERSION='0.42.2'

.PHONY: all
all: prepare lint test_rules pint_lint

.PHONY: prepare
prepare: pint
	./automation/promtool_tests.sh prepare

.PHONY: lint
lint:
	./automation/promtool_tests.sh lint

.PHONY: test_rules
test_rules:
	./automation/promtool_tests.sh test_rules

pint:
	echo 'Installing pint...'
	curl -OJL 'https://github.com/cloudflare/pint/releases/download/v${PINT_VERSION}/pint-${PINT_VERSION}-linux-amd64.tar.gz'
	tar xvzf 'pint-${PINT_VERSION}-linux-amd64.tar.gz' pint-linux-amd64
	mv pint-linux-amd64 pint
	rm 'pint-${PINT_VERSION}-linux-amd64.tar.gz'	
	
.PHONY: pint_lint
pint_lint:	
	./automation/pint_lint.sh
