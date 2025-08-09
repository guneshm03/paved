# Scorecard: Production readiness

- Entities evaluated: **6**
- Mean score: **78.9**
- Levels: gold=2 silver=2 bronze=1 none=1

## Entities

| Entity | Owner | Score | Level | Failing rules |
|---|---|---:|---|---|
| `component:checkout-web` | `group:default/team-growth` | 46.7 | none | `ci-runs-tests`, `readme`, `oncall`, `deps-fresh` |
| `component:catalog-service` | `group:default/team-growth` | 53.3 | bronze | `has-ci`, `ci-runs-tests`, `deps-fresh` |
| `component:ledger-service` | `group:default/team-payments` | 86.7 | silver | `deps-fresh` |
| `component:notification-worker` | `group:default/team-platform` | 86.7 | silver | `readme` |
| `component:auth-service` | `group:default/team-platform` | 100.0 | gold | - |
| `component:payments-service` | `group:default/team-payments` | 100.0 | gold | - |

## Rules

| Rule | Weight | Passed | Failed | Pass rate |
|---|---:|---:|---:|---:|
| Owned by a team in the catalog (`has-owner`) | 3 | 6 | 0 | 100.0% |
| Has a CI pipeline (`has-ci`) | 3 | 5 | 1 | 83.3% |
| CI runs the test suite (`ci-runs-tests`) | 2 | 4 | 2 | 66.7% |
| README explains the service (`readme`) | 2 | 4 | 2 | 66.7% |
| On-call link (`oncall`) | 2 | 5 | 1 | 83.3% |
| Dependencies reasonably fresh (`deps-fresh`) | 2 | 3 | 3 | 50.0% |
| Part of a system (`in-system`) | 1 | 6 | 0 | 100.0% |

## Team rollup

| Team | Owned | Mean | Subtree | Subtree mean | Min | Top failing |
|---|---:|---:|---:|---:|---:|---|
| `group:default/engineering` | 0 | - | 6 | 78.9 | 46.7 | deps-fresh(3) ci-runs-tests(2) readme(2) |
| &nbsp;&nbsp;`group:default/team-growth` | 2 | 50.0 | 2 | 50.0 | 46.7 | ci-runs-tests(2) deps-fresh(2) has-ci(1) |
| &nbsp;&nbsp;`group:default/team-payments` | 2 | 93.4 | 2 | 93.4 | 86.7 | deps-fresh(1) |
| &nbsp;&nbsp;`group:default/team-platform` | 2 | 93.4 | 2 | 93.4 | 86.7 | readme(1) |

## Findings

### `component:checkout-web` — 46.7

- **CI runs the test suite**: ci.runsTests = false, want == true — _Make CI run go test / pytest / npm test / make check._
- **README explains the service**: readme.words = 7, want >= 60 — _Write a README of at least 60 words covering purpose, running and operations._
- **On-call link**: oncall.link = "", want matches "^https://" — _Add the paved.dev/oncall-url annotation or a link with type oncall._
- **Dependencies reasonably fresh**: deps.outdatedRatio = 0.8, want <= 0.34 — _Upgrade outdated dependencies (see `paved score deps <entity>`)._

### `component:catalog-service` — 53.3

- **Has a CI pipeline**: ci.present = false, want == true — _Add a workflow under .github/workflows (or .gitlab-ci.yml / Jenkinsfile)._
- **CI runs the test suite**: ci.runsTests = false, want == true — _Make CI run go test / pytest / npm test / make check._
- **Dependencies reasonably fresh**: deps.outdatedRatio = 0.75, want <= 0.34 — _Upgrade outdated dependencies (see `paved score deps <entity>`)._

### `component:ledger-service` — 86.7

- **Dependencies reasonably fresh**: deps.outdatedRatio = 1, want <= 0.34 — _Upgrade outdated dependencies (see `paved score deps <entity>`)._

### `component:notification-worker` — 86.7

- **README explains the service**: readme.present = false, want == true — _Write a README of at least 60 words covering purpose, running and operations._
