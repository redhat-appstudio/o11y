FROM registry.access.redhat.com/ubi9/go-toolset:1.20.10-2 AS builder

# Set the working directory
WORKDIR /opt/app-root/src

COPY go.mod go.sum ./

# Download required dependencies
RUN go mod download
COPY --chown=default:root exporters/**/*.go .

RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/exporters .

EXPOSE 8090


FROM registry.access.redhat.com/ubi9-micro@sha256:1c8483e0fda0e990175eb9855a5f15e0910d2038dd397d9e2b357630f0321e6d

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
