# Orders Api

Accepts and tracks customer orders.

## Running locally

```sh
PORT=8080 go run .
curl localhost:8080/healthz
```

## Testing

`go test ./...` runs the unit tests; CI runs them with the race detector on
every push and pull request.

## Operations

Owned by `team-growth`.
