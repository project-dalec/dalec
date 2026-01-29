# Trace Replay Setup

This directory contains a Docker Compose setup to replay CI trace files locally
using Jaeger UI.

## Usage

Assumption: All below commands and run from the directory that the docker-compose yaml file is in.

1. Download the trace artifact from a CI run (e.g., `integration-test-reports-<suite>`)
2. Extract any/all `.jsonl` files to be processed under the `traces/` dir (which may not exist).
3. Run:
   ```bash
   docker compose up
   ```
4. Open Jaeger UI at http://localhost:16686

The otel-collector will read the `.jsonl` file and export traces to Jaeger. The
Jaeger UI can then be used to browse and analyze the traces.

Traces can be quite large so may take some time to process.

To customize the port that Jaeger UI listens on, you can set `JAEGER_UI_PORT`

## Files

- [docker-compose.yaml](./docker-compose.yaml) - Orchestrates Jaeger and OTEL Collector
- [otel-collector-config.yaml](./otel-collector-config.yaml) - Collector config for reading JSON files and exporting to Jaeger
- [jaeger-config.yaml](./jaeger-config.yaml) - Jaeger v2 configuration with memory storage
- [jaeger-ui-config.json](./jaeger-ui-config.json) - Jaeger UI configuration
