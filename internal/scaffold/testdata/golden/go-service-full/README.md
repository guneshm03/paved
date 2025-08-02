# Invoice Service

Generates and sends customer invoices.

## Running locally

```sh
PORT=9090 go run .
curl localhost:9090/healthz
```

## Testing

`go test ./...` runs the unit tests; CI runs them with the race detector on
every push and pull request.

## Operations

Owned by `team-payments`. On-call: https://oncall.acme.example/schedules/payments.
Schema migrations live in `migrations/` and are applied in lexical order.
