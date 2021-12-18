shikhandi is a tiny load generator for opentelemetry and heavily inspired by this archived [Omnition/synthetic-load-generator](https://github.com/Omnition/synthetic-load-generator).

## Usage

```sh
Usage: shikhandi <options>

Options:
    --collectorUrl string       OpenTelemetry collector URL (default "0.0.0.0:4317")
    --flushIntervalMillis int   How often to flush traces (default 5000)
    --topologyFile string       File describing the anatomy
```

## Build the binary

```sh
make binary
```
