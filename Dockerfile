FROM registry.access.redhat.com/ubi9/go-toolset:1.24.6-1758501173 AS builder

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

FROM registry.access.redhat.com/ubi9-minimal@sha256:7c5495d5fad59aaee12abc3cbbd2b283818ee1e814b00dbc7f25bf2d14fa4f0c

# Copy all compiled binaries from the builder stage to the final image.
COPY --from=builder /tmp/built_exporters/* /bin/

RUN microdnf install -y podman && microdnf clean all

RUN groupadd podman && useradd -u 1000 podman -g podman; \
usermod --add-subuids 100000-165535 --add-subgids 100000-165535 podman

VOLUME /home/podman/.local/share/containers

RUN chown podman:podman -R /home

RUN podman system migrate

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

USER podman

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]

# CMD is empty. The user must specify the exporter name as the first argument
# to the entrypoint when running the container.
CMD []
