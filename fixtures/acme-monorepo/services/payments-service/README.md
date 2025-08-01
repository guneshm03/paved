# Payments service

The payments service authorises and captures card payments for every Acme
storefront. It exposes the Payments API (OpenAPI), records every movement in
the ledger through the Ledger API, and publishes payment lifecycle events to
the shared event bus so that notifications and analytics can react.

## Running locally

Start Postgres with the compose file in this directory, export the database
URL, and run the service. Card networks are replaced by a deterministic
simulator unless a live acquirer key is configured.

## Operations

Alerts page the Payments on-call rotation. Dashboards and runbooks are linked
from the catalog entry. Refund failures and authorisation latency are the two
service-level indicators that matter most.

## Ownership

Owned by team-payments. Changes to the refund flow need a second reviewer from
the team because of PCI scope.
