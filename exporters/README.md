# Multi-Exporter Docker Image

This repository contains the configuration to build a Docker image that includes multiple Go-based observability exporters. The image is designed to automatically discover and compile each exporter from a defined directory structure, and allows you to select which exporter to run at container startup.

## Features

* **Dynamic Multi-Exporter Build:** The `Dockerfile` automatically finds and compiles all Go main packages located within direct subdirectories of the `exporters/` source folder.
* **Runtime Selection:** An entrypoint script (`entrypoint.sh`) enables you to specify which compiled exporter to run when starting the container.
* **Consolidated Image:** Packages multiple exporter tools into a single, flexible Docker image.

**Key Points**:

* All Go exporter source code must reside within its own subdirectory under the main `exporters/` directory (e.g., `exporters/dsexporter/`).
* Each such subdirectory (e.g., `dsexporter`) must contain a Go `main` package (i.e., `package main` and a `func main()`).
* The `Dockerfile` will build each of these subdirectories into an executable binary named after the subdirectory (e.g., `dsexporter/` becomes `/bin/dsexporter` in the final image).
