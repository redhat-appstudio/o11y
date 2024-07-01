FROM registry.access.redhat.com/ubi9/go-toolset:1.21.10-1.1719562237 AS builder

# Set the working directory
WORKDIR /opt/app-root/src

COPY go.mod go.sum ./

# Download required dependencies
RUN go mod download
COPY --chown=default:root exporters/**/*.go .

RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/exporters .

EXPOSE 8090


FROM registry.access.redhat.com/ubi9-micro@sha256:2044e2ca8e258d00332f40532db9f55fb3d0bfd77ecc84c4aa4c1b7af3626ffb

COPY --from=builder /bin/exporters /bin/exporters

# It is mandatory to set these labels
LABEL name="Konflux Observability Exporters"
LABEL description="Konflux Observability Exporters"
LABEL com.redhat.component="Konflux Observability Exporters"
LABEL io.k8s.description="Konflux Observability Exporters"
LABEL io.k8s.display-name="o11y-exporters"
LABEL io.openshift.tags="konflux"
LABEL summary="Konflux Observability Exporters"

CMD ["/bin/exporters"]
