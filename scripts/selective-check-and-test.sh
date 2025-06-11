#!/bin/bash

set -e

# --- Argument Handling ---
if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <command_string> <rule_files_string> <test_case_files_string>"
    echo "Example: $0 \"my-checker --option\" \"rule1.yml rule2.yml\" \"testA.json testB.json\""
    exit 1
fi

COMMAND_TO_RUN="$1"
RULE_FILES_LIST="$2"
TEST_CASE_FILES_LIST="$3"


TEMP_DIR=$(mktemp -d "$(pwd)/selective_rules_check_temp.XXXXXX")

# Setup a trap to ensure cleanup of TEMP_DIR on script exit (normal, error, or interrupt)
trap 'echo "Cleaning up ${TEMP_DIR}/ directory..."; rm -rf "${TEMP_DIR}"; echo "Cleanup finished.";' EXIT

TEMP_DIR_BASENAME=$(basename "${TEMP_DIR}")

echo "Setting up temporary directories ${TEMP_DIR}/rules/ and ${TEMP_DIR}/tests/..."
echo "Full temporary directory path: ${TEMP_DIR}"
echo "Basename of temporary directory: ${TEMP_DIR_BASENAME}"

mkdir -p "${TEMP_DIR}/rules/"
mkdir -p "${TEMP_DIR}/tests/"

echo "Copying rule files: ${RULE_FILES_LIST}"
for rule_file in ${RULE_FILES_LIST}; do
    if [ -f "${rule_file}" ]; then
        # Preserve the original error handling for cp
        cp "${rule_file}" "${TEMP_DIR}/rules/" || { echo "Error during rule file copy: ${rule_file}"; exit 1; }
    else
        echo "Warning: Rule file ${rule_file} not found. Skipping." >&2
    fi
done

echo "Copying test files: ${TEST_CASE_FILES_LIST}"
for test_file in ${TEST_CASE_FILES_LIST}; do
    if [ -f "${test_file}" ]; then
        cp "${test_file}" "${TEMP_DIR}/tests/" || { echo "Error during test file copy: ${test_file}"; exit 1; }
    else
        echo "Warning: Test file ${test_file} not found. Skipping." >&2
    fi
done

echo "Running checker command: ${COMMAND_TO_RUN} ..."
${COMMAND_TO_RUN} -d "/work/${TEMP_DIR_BASENAME}/rules/" -y -p --tests-dir "/work/${TEMP_DIR_BASENAME}/tests/"

# The cleanup is handled by the trap EXIT signal.
echo "Checker command processing finished."