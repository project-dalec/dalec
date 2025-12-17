#!/usr/bin/env bash

set -eu -o pipefail

# Use the bake target for the image
# This makes it so dependabot can automatically update the image for us.
# OTEL_COLLECTOR_REF is read in the bake file to set the image tag for the otel-collector image.
export OTEL_COLLECTOR_REF=local/ci/otel-collector:latest 
docker buildx bake otel-collector

TRACES_PATH="$(mktemp -d)"
chmod 777 "${TRACES_PATH}" # otel container runs as non-root

id="$(docker run -d \
		--mount "type=bind,source=$(pwd)/.github/workflows/otel-config.yml,target=/etc/otelcol-contrib/config.yaml" \
		--mount "type=bind,source=${TRACES_PATH},target=/data" \
		--net=host \
		--restart=always \
		${OTEL_COLLECTOR_REF} \
			--config file:/etc/otelcol-contrib/config.yaml)"

echo "traces-path=${TRACES_PATH}" >> "${GITHUB_OUTPUT}"
echo "tracing-id=${id}" >> "${GITHUB_OUTPUT}"

docker0_ip="$(ip -f inet addr show docker0 | grep -Po 'inet \K[\d.]+')"
OTEL_EXPORTER_OTLP_ENDPOINT="http://${docker0_ip}:4318"
echo "OTEL_EXPORTER_OTLP_ENDPOINT=${OTEL_EXPORTER_OTLP_ENDPOINT}" >> "${GITHUB_ENV}"

OTEL_SERVICE_NAME="dalec-integration-test"
echo "OTEL_SERVICE_NAME=${OTEL_SERVICE_NAME}" >> "${GITHUB_ENV}"

OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
echo "OTEL_EXPORTER_OTLP_PROTOCOL=${OTEL_EXPORTER_OTLP_PROTOCOL}" >> "${GITHUB_ENV}"

tmp="$(mktemp)"
echo "[Service]" > "${tmp}"
echo "Environment=\"OTEL_EXPORTER_OTLP_ENDPOINT=${OTEL_EXPORTER_OTLP_ENDPOINT}\"" >> "${tmp}"
echo "Environment=\"OTEL_EXPORTER_OTLP_PROTOCOL=${OTEL_EXPORTER_OTLP_PROTOCOL}\"" >> "${tmp}"

sudo mkdir -p /etc/systemd/system/docker.service.d
sudo mkdir -p /etc/systemd/system/containerd.service.d
sudo cp "${tmp}" /etc/systemd/system/docker.service.d/otlp.conf
sudo cp "${tmp}" /etc/systemd/system/containerd.service.d/otlp.conf

sudo systemctl daemon-reload
sudo systemctl restart containerd
sudo systemctl restart docker
