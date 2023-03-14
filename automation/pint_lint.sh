#!/bin/bash -e

main() {
    local extracted_rule_file=test/promql/extracted-rules.yaml
    
    install_pint

    echo "Linting Prometheus rules..."
    ./pint --no-color lint $extracted_rule_file
}

install_pint() {
    echo "Installing pint..."
    VERSION="0.42.1"
    curl -OJL "https://github.com/cloudflare/pint/releases/download/v${VERSION}/pint-$VERSION-linux-amd64.tar.gz"     
    tar xvzf pint-$VERSION-linux-amd64.tar.gz pint-linux-amd64 && mv pint-linux-amd64 pint
    rm pint-$VERSION-linux-amd64.tar.gz
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
