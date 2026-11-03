# Building Load Balancers in Go

A simple HTTP reverse-proxy load balancer written in Go 

It distributes incoming requests across backends by weight, retries failed proxies, and runs **TCP health checks** so traffic only goes to servers that are up.

## Features

- Smooth weighted round-robin selection
- Optional per-backend weights via `url|weight`
- Skip unhealthy backends
- Retry the same backend a few times, then try another
- Mark backends down on proxy errors
- Periodic TCP health checks to recover backends
- CLI flags for backends and listen port

## Requirements

- Go 1.22+

## Run

Equal weights (omit `|weight`, default is `1`):

```bash
go run . -backends=http://127.0.0.1:8081,http://127.0.0.1:8082 -port=3030
```

Weighted (8081 gets 3x the traffic of 8082):

```bash
go run . -backends="http://127.0.0.1:8081|3,http://127.0.0.1:8082|1" -port=3030
```

**PowerShell note:** `|` is the pipe operator in PowerShell. Always quote the `-backends` value when using weights, as shown above.

| Flag | Description | Default |
|------|-------------|---------|
| `-backends` | Comma-separated `url` or `url|weight` (required) | none |
| `-port` | Port the load balancer listens on | `3030` |

## Try it locally

1. Start two dummy backends (separate terminals):

```bash
python -m http.server 8081
python -m http.server 8082
```

2. Start the load balancer with weights:

```bash
go run . -backends="http://127.0.0.1:8081|3,http://127.0.0.1:8082|1" -port=3030
```

3. Send requests:

```bash
curl http://127.0.0.1:3030/
```

If both backends are down, the balancer returns `Service not available` (HTTP 503). Health checks run every 2 minutes and mark backends up/down based on a TCP dial.

## Tests

```bash
go test ./...
```

## Project layout

| File | Purpose |
|------|---------|
| `main.go` | Load balancer, weighted peer selection, health checks, CLI |
| `main_test.go` | Round-robin and dead-backend tests |
| `go.mod` | Module definition |

## How it works

1. Parse backend URLs (and optional weights) and create a `ReverseProxy` per backend.
2. On each request, pick a peer with smooth weighted round robin: raise each alive backend's current weight by its weight, choose the highest, then subtract the total.
3. On proxy errors, retry, then mark the backend down and try another peer.
4. A background goroutine dials each backend over TCP and updates `Alive`.

Weighted selection follows the nginx smooth weighted round-robin approach.