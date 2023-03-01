#!/bin/bash -e

main() {
    ls -l
    install_promtool

    echo "Running tests"
    ./promtool test rules test/promql/*
}

install_promtool() {
    echo "Installing promtool..."
    VERSION="2.42.0"
    curl -OJL "https://github.com/prometheus/prometheus/releases/download/v${VERSION}/prometheus-$VERSION.linux-amd64.tar.gz"
    tar xvzf "prometheus-$VERSION.linux-amd64.tar.gz" "prometheus-$VERSION.linux-amd64"/promtool --strip-components=1
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
