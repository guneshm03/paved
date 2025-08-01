# Auth service

Issues and validates the tokens every Acme service uses: customer sessions for
the storefront and short-lived service tokens for internal calls. Sessions are
held in the shared Redis cluster; signing keys rotate weekly.

## Using it

Services should call the introspection endpoint through the shared client
rather than parsing tokens themselves, so key rotation stays transparent.

## Operations

Pages the Platform on-call. An outage blocks every login and most internal
traffic, so the service runs in three zones with a strict error budget.
