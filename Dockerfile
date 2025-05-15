FROM registry.access.redhat.com/ubi9/go-toolset:1.23.6-1747059472 AS builder

# Set the working directory
WORKDIR /opt/app-root/src

COPY go.mod go.sum ./

# Download required dependencies
RUN go mod download
COPY --chown=default:root exporters/**/*.go .

RUN CGO_ENABLED=0 GOOS=linux go build -o /tmp/exporters .

EXPOSE 8090


FROM registry.access.redhat.com/ubi9-micro@sha256:93ae5bced8702423154dea0bf6785d5e2c42788dabac39d288e0ca23afd88884

COPY --from=builder /tmp/exporters /bin/exporters

# It is mandatory to set these labels
LABEL name="Konflux Observability Exporters"
LABEL description="Konflux Observability Exporters"
LABEL com.redhat.component="Konflux Observability Exporters"
LABEL io.k8s.description="Konflux Observability Exporters"
LABEL io.k8s.display-name="o11y-exporters"
LABEL io.openshift.tags="konflux"
LABEL summary="Konflux Observability Exporters"

CMD ["/bin/exporters"]
