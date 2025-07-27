# Acme fixture monorepo

A small, fictional engineering organisation used by paved's tests and `make demo`.
Nothing here describes a real company; all package names live in the `example.com`,
`@acme` and `acme-*` namespaces and every version/date in `.paved/registry.yaml` is invented.

## Contents (29 entities in 13 descriptor files)

- `org/` — Groups: `engineering` (department) with `team-payments`, `team-platform`,
  `team-growth` (which has sub-team `team-data`) and `team-security`.
- `systems/` — Systems `billing`, `storefront`, `platform-core`.
- `infra/` — Resources: databases, a cache, the event bus, and a legacy SFTP server.
- `services/*` — eight Components (with their APIs in the same multi-document descriptor),
  each with a different mix of CI (GitHub Actions, GitLab CI, Jenkins, none), README quality,
  on-call links, CODEOWNERS and dependency freshness (go.mod / package.json / requirements.txt).
- `libs/payments-sdk` — a library that is a `subcomponentOf` payments-service.
- `platform/events` — an AsyncAPI contract.
- `.paved/` — scorecards (`production-readiness`, `hygiene`), templates (`go-service`,
  `python-worker`) and the synthetic registry snapshot. Hidden directories are not scanned for
  descriptors, so template skeletons do not leak into the catalog.

## Deliberate problems (asserted by tests)

| Problem | Where | Detected as |
|---|---|---|
| Owner team was disbanded | `services/legacy-reporting` (`owner: team-reporting`) | `dangling-ref` error; orphans `no-owner`, `no-system` |
| API nobody provides | `platform/events` (`events-api`) | orphan `unprovided-api` |
| Resource nothing uses, outside any system | `infra` (`legacy-ftp`) | orphans `unused-resource`, `no-system` |
| Team that owns nothing and has no members | `org` (`team-security`) | orphan `empty-group` |
| No CI, unpinned dependency | `services/catalog-service` | scorecard `has-ci`, `deps-pinned` fail |
| No README | `services/notification-worker` | scorecard `readme` fails |
| Outdated dependencies (2 majors behind) | `services/checkout-web`, `services/ledger-service` | `deps-fresh`, `libyears` fail |
