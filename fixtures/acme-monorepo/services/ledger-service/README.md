# Ledger service

Double-entry bookkeeping for Acme. Every payment, refund and payout is posted
as a balanced pair of entries; balances are derived, never stored.

## API

The gRPC Ledger API is defined in proto/ledger.proto. Posting is idempotent on
the client-supplied transaction id, so retries are always safe.

## Operations

Pages the Payments on-call. The nightly reconciliation job compares derived
balances with the acquirer settlement files and opens an incident on drift.
