shikhandi is a tiny load generator for opentelemetry and heavily inspired by this archived [Omnition/synthetic-load-generator](https://github.com/Omnition/synthetic-load-generator).

## Usage

```sh
Usage: shikhandi <options>

Options:
    --topologyFile      string      File describing the anatomy
    --serviceNamespace  string      Set OtelCollector resource attribute: service.namespace
    --pprofAddress      string      Address of pprof server
```

For Processor/Exporter use the same settings supported OpenTelemetry

A secure gRPC connection for OTLP exporter would use something like following.
```
OTEL_EXPORTER_OTLP_ENDPOINT=https://127.0.0.1:4317
OTEL_EXPORTER_OTLP_CERTIFICATE=/path/to/ca/cert.pem
```
And seeting scheme to `http` instructs exporter to use insecure connection.

## Build the binary

```sh
make binary
```
