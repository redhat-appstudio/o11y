#!/bin/bash -e

main() {
    ls -l
    echo "Running tests"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
