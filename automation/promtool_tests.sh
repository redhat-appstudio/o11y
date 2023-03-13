#!/bin/bash -ex

set -o pipefail

main() {
    local rule_file=prometheus/base/prometheus.rules.yaml
    local extracted_rule_file=test/promql/extracted-rules.yaml

    case "$1" in

    prepare)
        install_promtool
        install_yq

        echo "Extract Prometheus rules"
        ./yq ".spec" $rule_file > $extracted_rule_file
        ;;

    lint)
        echo "Running Prometheus rules linter tests"
        ./promtool check rules $extracted_rule_file
        ;;

    test_rules)
        echo "Running Prometheus rules unit tests"
        ./promtool test rules test/promql/tests/*
        ;;

    *)
        echo "Unrecognized option $1"
        exit 1
        ;;
    esac
}

install_promtool() {
    if test -f "./promtool"; then
        echo "Skipping promtool installation"
        return
    fi
    echo "Installing promtool..."
    VERSION="2.42.0"
    curl -OJL "https://github.com/prometheus/prometheus/releases/download/v${VERSION}/prometheus-$VERSION.linux-amd64.tar.gz"
    tar xvzf "prometheus-$VERSION.linux-amd64.tar.gz" "prometheus-$VERSION.linux-amd64"/promtool --strip-components=1
    rm -f prometheus-$VERSION.linux-amd64.tar.gz
}

install_yq() {
    if test -f "./yq"; then
        echo "Skipping yq installation"
        return
    fi
    echo "Installing yq..."
    VERSION="4.31.2"
    curl -OJL "https://github.com/mikefarah/yq/releases/download/v$VERSION/yq_linux_amd64.tar.gz"
    tar xvzf yq_linux_amd64.tar.gz ./yq_linux_amd64
    rm -f yq_linux_amd64.tar.gz
    mv yq_linux_amd64 yq
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
