FROM registry.access.redhat.com/ubi9/go-toolset:1.25.9-1777537863 AS builder

USER root

# Set the working directory for build operations.
WORKDIR /opt/app-root/src

COPY go.mod go.sum ./

# Copy the 'exporters' source directory, preserving its subdirectory structure.
# This structure is crucial for the automated build loop below.
COPY --chown=default:root exporters ./exporters

# Create a dedicated directory for the compiled binaries.
RUN mkdir -p /tmp/built_exporters

# Automate building all Go main packages found in direct subdirectories of ./exporters.
# Each subdirectory is assumed to contain a 'main' package.
# The output binary will be named after its source subdirectory.
RUN cd exporters && \
    for dir in */; do \
        if [ -d "$dir" ]; then \
            exporter_name=$(basename "$dir") && \
            echo "Building exporter: ${exporter_name} from directory ${dir}" && \
            (cd "$dir" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o "/tmp/built_exporters/${exporter_name}" .) && \
            echo "Successfully built ${exporter_name}" || exit 1; \
        fi \
    done

# Oras binary from konflux image
FROM quay.io/konflux-ci/oras:latest@sha256:c4fb02d6c1caa722360e0317002d5dfc2fce8a465bb3a3fdd62a3d1608105cbc as oras

FROM registry.access.redhat.com/ubi9-micro@sha256:fdf68a4f5f88cca14ae906bbec6e0fbbffe92b5b91e73e0862c961234d63b986

# Copy oras binary from the oras image to the final image.
COPY --from=oras /bin/oras /bin/oras

# Copy all compiled binaries from the builder stage to the final image.
COPY --from=builder /tmp/built_exporters/* /bin/

# Copy the CA certificates
COPY --from=builder /etc/pki/ca-trust/extracted/ /etc/pki/ca-trust/extracted/

# Copy the entrypoint script and ensure it's executable.
COPY exporter-build-scripts/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# It is mandatory to set these labels
LABEL name="Konflux Observability Exporters"
LABEL description="Konflux Observability Exporters"
LABEL com.redhat.component="Konflux Observability Exporters"
LABEL io.k8s.description="Konflux Observability Exporters"
LABEL io.k8s.display-name="o11y-exporters"
LABEL io.openshift.tags="konflux"
LABEL summary="Konflux Observability Exporters"

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]

# CMD is empty. The user must specify the exporter name as the first argument
# to the entrypoint when running the container.
CMD []
