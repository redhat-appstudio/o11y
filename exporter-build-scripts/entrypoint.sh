#!/bin/sh
set -e

EXPORTER_CHOICE="${1}"

if [ -z "${EXPORTER_CHOICE}" ]; then
  echo "Error: No exporter specified." >&2
  echo "Usage: <exporter_name> [exporter_specific_args...]" >&2
  echo "Example for dsexporter: dsexporter" >&2
  echo "Available exporters are named after the subdirectories in your 'o11y/exporters'." >&2
  exit 1
fi

shift

echo "Attempting to start exporter: ${EXPORTER_CHOICE}"

# Construct the path to the binary
BINARY_PATH="/bin/${EXPORTER_CHOICE}"

if [ ! -x "${BINARY_PATH}" ]; then
    echo "Error: Exporter binary '${BINARY_PATH}' not found or not executable." >&2
    echo "Ensure '${EXPORTER_CHOICE}' matches a compiled exporter name." >&2
    exit 1
fi

exec "${BINARY_PATH}" "$@"