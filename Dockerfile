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

# Prepare buildah settings
FROM quay.io/konflux-ci/buildah-task:latest AS buildah-prepare

FROM registry.access.redhat.com/ubi9-minimal@sha256:7c5495d5fad59aaee12abc3cbbd2b283818ee1e814b00dbc7f25bf2d14fa4f0c

# Copy all compiled binaries from the builder stage to the final image.
COPY --from=builder /tmp/built_exporters/* /bin/
# ******************************************************************************
# registry monitoring setup

RUN rpm --setcaps shadow-utils && \
    microdnf install -y buildah shadow-utils fuse-overlayfs && microdnf clean all

COPY --from=buildah-prepare /etc/containers/ /etc/containers/

RUN mkdir -p /var/lib/shared/overlay-images \
             /var/lib/shared/overlay-layers \
             /var/lib/shared/vfs-images \
             /var/lib/shared/vfs-layers && \
    touch /var/lib/shared/overlay-images/images.lock && \
    touch /var/lib/shared/overlay-layers/layers.lock && \
    touch /var/lib/shared/vfs-images/images.lock && \
    touch /var/lib/shared/vfs-layers/layers.lock

RUN useradd build && \
    echo -e "root:1:4294967294\nbuild:1:999\nbuild:1001:4294967294" > /etc/subuid && \
    echo -e "root:1:4294967294\nbuild:1:999\nbuild:1001:4294967294" > /etc/subgid && \
    mkdir -p /home/build/.local/share/containers && \
    mkdir -p /home/build/.config/containers && \
    chown -R build:build /home/build && \
    chown -R build:build /home

# RUN sed -e 's|^#mount_program|mount_program|g' \
        # -e 's|^graphroot|#graphroot|g' \

RUN sed -i -r -e 's,driver = ".*",driver = "vfs",g' /etc/containers/storage.conf

RUN sed -e 's|^graphroot|#graphroot|g' \
        -e 's|^runroot|#runroot|g' \
        /etc/containers/storage.conf \
        > /home/build/.config/containers/storage.conf && \
        chown build:build /home/build/.config/containers/storage.conf

VOLUME /var/lib/containers
VOLUME /home/build/.local/share/containers

# Set an environment variable to default to chroot isolation for RUN
# instructions and "buildah run".
ENV BUILDAH_ISOLATION=chroot

# ******************************************************************************


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

USER build

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]

# CMD is empty. The user must specify the exporter name as the first argument
# to the entrypoint when running the container.
CMD []
