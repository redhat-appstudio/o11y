#!/bin/bash -ex

main() {
    install_promtool
    install_yq

    echo "Extract Prometheus rules"
    ./yq ".spec" prometheus/base/prometheus.rules.yaml > test/promql/extracted-rules.yaml

    echo "Running tests"
    ./promtool test rules test/promql/tests/*
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
