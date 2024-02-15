FROM registry.access.redhat.com/ubi9/go-toolset:1.20.10-2 AS builder

# Set the working directory
WORKDIR /opt/app-root/src

COPY go.mod go.sum ./

# Download required dependencies
RUN go mod download
COPY --chown=default:root exporters/**/*.go .

RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/exporters .

EXPOSE 8090

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.3-1361.1699548032
RUN microdnf update --setopt=install_weak_deps=0 -y && microdnf install -y libcurl-minimal libcurl-devel

COPY --from=builder /bin/exporters /bin/exporters

# It is mandatory to set these labels
LABEL description="Konflux Observability Exporters"
LABEL io.k8s.description="Konflux Observability Exporters"
LABEL io.k8s.display-name="o11y-exporters"
LABEL io.openshift.tags="konflux"
LABEL summary="Konflux Observability Exporters"

CMD ["/bin/exporters"]
