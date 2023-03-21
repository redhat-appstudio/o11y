#!/bin/bash -e

main() {
    local extracted_rule_file=test/promql/extracted-rules.yaml
    
    echo "Linting Prometheus rules..."
    ./pint --no-color lint $extracted_rule_file
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
