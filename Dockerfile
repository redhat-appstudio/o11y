FROM registry.access.redhat.com/ubi9/go-toolset:1.23.6-1747333074 AS builder

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


FROM registry.access.redhat.com/ubi9-micro@sha256:955512628a9104d74f7b3b0a91db27a6bbecdd6a1975ce0f1b2658d3cd060b98

# Copy all compiled binaries from the builder stage to the final image.
COPY --from=builder /tmp/built_exporters/* /bin/

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