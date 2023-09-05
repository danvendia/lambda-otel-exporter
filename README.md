# lambda-otel-exporter
A Lambda extension for receiving OTEL traces, batching the traces, and sending it to a destination.

# Development

## Prerequisites
* `brew install golang`
* `brew install golangci-lint`

## Running Locally
```
go run main.go -localMode
```

## Testing
```
go test -v ./...
```

## Linting
```
golangci-lint run
```

## Building Extension
```
./scripts/build-extension.sh
```

