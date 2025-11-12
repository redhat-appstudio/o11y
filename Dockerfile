FROM registry.access.redhat.com/ubi9/go-toolset:1.24.6-1762373805 AS builder

USER root

# Set the working directory for build operations.
WORKDIR /opt/app-root/src

COPY go.mod go.sum ./

# Download Go module dependencies.
RUN go mod download

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

# Download oras binary for registry exporter
RUN curl -LO "https://github.com/oras-project/oras/releases/download/v1.3.0/oras_1.3.0_linux_amd64.tar.gz" \
    && mkdir -p /tmp/oras-install/ \
    && tar -zxf oras_1.3.0_linux_amd64.tar.gz -C /tmp/oras-install/

FROM registry.access.redhat.com/ubi9-micro@sha256:e14a8cbcaa0c26b77140ac85d40a47b5e910a4068686b02ebcad72126e9b5f86

# Copy all compiled binaries from the builder stage to the final image.
COPY --from=builder /tmp/built_exporters/* /tmp/oras-install/oras /bin/

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
