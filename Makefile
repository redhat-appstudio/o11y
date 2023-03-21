.PHONY: all
all: prepare lint test_rules

.PHONY: prepare
prepare:
	./automation/promtool_tests.sh prepare

.PHONY: lint
lint:
	./automation/promtool_tests.sh lint

.PHONY: test_rules
test_rules:
	./automation/promtool_tests.sh test_rules

.PHONY: pint
pint:
	./automation/pint_lint.sh
