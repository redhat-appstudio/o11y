FROM registry.access.redhat.com/ubi9/go-toolset:1.22.9-1739801907 AS builder

# Set the working directory
WORKDIR /opt/app-root/src

COPY go.mod go.sum ./

# Download required dependencies
RUN go mod download
COPY --chown=default:root exporters/**/*.go .

RUN CGO_ENABLED=0 GOOS=linux go build -o /tmp/exporters .

EXPOSE 8090


FROM registry.access.redhat.com/ubi9-micro@sha256:91828a5ca154132e18072cf885f6bb41bb881e6158f30b793f4b424ec144b87d

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
