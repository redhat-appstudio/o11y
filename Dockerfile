FROM registry.access.redhat.com/ubi9/go-toolset:1.20.10-2 AS builder

# Set the working directory
WORKDIR /opt/app-root/src

COPY go.mod go.sum ./

# Download required dependencies
RUN go mod download
COPY --chown=default:root exporters/**/*.go .

RUN ["go", "build", "."]

EXPOSE 8090
CMD ["go", "run", "."]


FROM registry.access.redhat.com/ubi8/ubi-minimal:8.6-751
RUN microdnf update --setopt=install_weak_deps=0 -y && microdnf install libcurl-minimal libcurl-devel
