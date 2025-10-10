# Development

## Local deployment

### Manual
The exporter can be run with the following commands:

1. Generate the docker config.json file with secret token and store it in `${PATH_TO_CONFIG}`
1. Set the url to the registry in `${QUAY_URL}`
1. Setup a test directory `${PATH_TO_TEST_DIR}` 
1. Build the exporter image locally by running:
    - `podman build . -t quay-exporter`
1. Run the image locally
    - `podman run -it -p 9101:9101 -e=DOCKER_CONFIG="/.docker/" -e=QUAY_URL=${QUAY_URL} -v ${PATH_TO_CONFIG}:/.docker/config.json -v ${PATH_TO_TEST_DIR}:/mnt/storage localhost/quay-exporter registryexporter`

This will be documentation for registry monitoring exporter (monitoring for quay, images.paas, etc.), explanations of the modules, background information, and implementation details.

Still subject to name changes...
