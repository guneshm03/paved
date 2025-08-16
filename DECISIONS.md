# Design decisions

Key design decisions in paved, each with the reasoning and what it would cost if it turned out wrong.
Format: `<what> — <why> — <cost if wrong>`.

## Catalog

- Support exactly Component, API, System, Group, Resource; any other kind is an `unsupported-kind` error and the document is skipped — matches the supported subset and keeps reference resolution total — Catalogs that use `User`/`Domain`/`Location` get errors and need those kinds added.
- Owners must be Groups; `user:` owners are rejected as an unsupported kind — no User kind exists to resolve against, and team ownership is what scorecard rollups need — Orgs that assign individuals as owners would see validation errors.
- `dependsOn` references must state their kind (`component:`/`resource:`) — same rule as Backstage; a bare name is ambiguous between the two allowed kinds — Slightly more verbose descriptors.
- Names/namespaces are compared case-insensitively and stored lower-cased — Backstage semantics — Display of mixed-case names in refs is lower-cased.
- Unresolvable references are errors and create no graph edge — keeps every graph query well-defined and makes dangling owners visible as orphans — A catalog mid-migration (owner group not yet added) fails `catalog validate` until fixed.
- A group whose parent does not list it in `children` (or vice versa) is a warning, but both directions still create the edge — Backstage tolerates one-sided declarations; the warning flags drift — Users may ignore the warning.
- `dependsOn` cycles are warnings, group-hierarchy cycles are errors — dependency cycles exist in real systems; ownership cycles make escalation chains meaningless — None significant; walks are cycle-safe either way.
- Discovery skips all hidden directories plus `node_modules` and `vendor` — template skeletons under `.paved/templates` contain `catalog-info.yaml` files that must not enter the catalog — A descriptor deliberately placed in a hidden directory is ignored.
- "Orphan" means structurally detached (no-owner, no-system, unprovided-api, unused-resource, empty-group), not Backstage's "location removed" — paved has no location/ingestion history, and these five are what platform teams actually chase — Users expecting Backstage's meaning must read the docs.
- "What depends on Y" includes components that consume an API Y provides (the API is traversed, not reported as a hit) — that is the dependency people mean when they ask about blast radius — A consumer that only uses part of an API is still reported.
- When `spec.owner` is missing or dangling, effective ownership is inherited from `subcomponentOf`, then `system`, and the answer reports `via`/`inheritedFrom` — avoids false "unowned" for sub-components while staying explicit — A wrongly inherited owner could be paged; the `via` field makes that visible.

## Scaffolder

- Only files ending in `.tmpl` have their content rendered (suffix stripped); every path segment is always rendered — keeps Go/Python tooling from choking on template syntax in skeletons and lets binary/literal `{{` files pass through untouched — Template authors must remember the suffix.
- A path segment rendering to an empty string drops the file — simplest way to express optional files/directories without a separate manifest — A template bug that renders an empty segment silently skips a file (golden tests catch it).
- Unsupported JSON-Schema keywords (`$ref`, `oneOf`, …) are compile errors, not ignored — otherwise a template could appear to validate input it does not — Templates written for full JSON Schema need simplifying.
- Optional parameters without a default are exposed to templates as `nil`; `missingkey=error` stays on — `{{ if .oncallUrl }}` must work, but typos in variable names must fail loudly — None found.
- The REST API offers dry-run only; applying is CLI-only — an API that writes to the server's disk is a security and multi-user hazard — Self-service portals would need a separate "open a PR" step (roadmap).
- A real apply is refused when the generated `catalog-info.yaml` has validation errors against the live catalog (dry-run reports them and exits 1) — scaffolding a service owned by a non-existent team is the classic IDP failure — Bootstrapping a brand-new team requires adding the Group first.
- Generated-descriptor checks drop existing entities declared in a `catalog-info.yaml` the render overwrites (CLI default target / API dry-run); with a `--target` that is not the catalog root (compared after `Abs`+`EvalSymlinks`), every loaded entity is kept (`CheckDescriptorsAlongside`) — the overwritten declaration is being replaced, so counting it made every re-scaffold a false `duplicate-entity` and `--force` unusable for existing services, while writing into another tree really does add a second declaration — A rename inside a re-scaffolded descriptor is validated as a fresh entity; references *to* the old name elsewhere surface only on the next full `paved validate`.
- Template helpers are deterministic (no `now`, `env`, random) — golden tests and reviewable diffs depend on it — Templates cannot stamp dates; pass them as parameters.

## Facts and scorecards

- Facts are collected from the descriptor's directory only — monorepo layouts put each service's catalog file at its root; no git or network access needed — Polyrepo setups with descriptors elsewhere need a source-dir annotation (not implemented).
- Dependency freshness uses an offline registry snapshot file with release dates — reproducible scores, no network in tests/CI, and it enables libyears — Someone must refresh the snapshot; the bundled one is synthetic.
- npm/PyPI version ranges are reduced to their lower bound (`^1.2.3` → 1.2.3) — what a fresh install at minimum gets; lockfiles are out of scope — Freshness may be understated for projects whose lockfile resolves newer versions.
- Go `/vN` module suffixes share one release history in the registry — so `majorsBehind` works across major-version module paths — A module that genuinely changed owners between majors would be conflated.
- A rule referencing a fact that is absent fails (except `exists`) — missing evidence should not pass a readiness check — Rules that should "pass when not applicable" must say so with `any: [{fact: deps.present, equals: false}, …]` (the bundled hygiene card does).
- Score = weighted pass percentage; levels are score thresholds (not "all rules of tier N pass") — simple to explain and to aggregate in rollups — Some orgs prefer gated tiers; would be a different level mode.
- Team rollups use the *effective* owner (including inheritance) and aggregate up the group tree with both direct and subtree stats — managers need their whole org; teams need their own slice — Inherited ownership could attribute a score to the wrong team; `owner.via` is exposed as a fact.

## Engineering

- Single third-party dependency (`go.yaml.in/yaml/v3`, the maintained yaml.v3 fork); diff, schema validation, version ordering, rule DSL and routing are in-repo or stdlib — keeps the supply chain tiny and the algorithms reviewable — More code to maintain than pulling libraries.
- golangci-lint's errcheck excludes `fmt.Fprint*` — those writes go to `strings.Builder`/`tabwriter` (errors surface at `Flush`, which is checked) or to CLI stdout where nothing useful can be done on failure — A broken stdout pipe is not reported as an error exit.
- The API converts the mux's 404/405 into JSON by probing the mux with `httptest.NewRecorder` — keeps Go 1.22 method-mismatch (405 + `Allow`) behaviour while returning the error envelope — Uses a test-helper package in production code; a hand-written recorder would remove that.
- Coverage gate is 85% on `internal/...` (contract minimum is 80%) — the code is mostly pure logic and is at ~95%; 85% leaves room for honest error-path gaps — A future change that drops coverage must add tests.
- `make check` runs the race detector on all tests — the API swaps snapshots concurrently — Slower than plain `go test` (~15 s total on an M1 Pro).
