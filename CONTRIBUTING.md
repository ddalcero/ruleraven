# Contributing to RuleRaven

> **Project state:** Alpha / under active development. The repository does not
> yet contain a working controller. Commands and package paths marked as
> "planned" become authoritative only when their implementation lands.

Thank you for helping build RuleRaven. Changes should preserve its core promise:
explainable Kubernetes incident triage with no automatic remediation and the
least cluster access necessary.

## Before you start

- Search existing issues and pull requests before proposing overlapping work.
- For a behavior change, open or reference an issue that states the failure
  mode, expected behavior, and safety impact.
- Keep pull requests narrow. Architecture changes should update the MVP plan or
  include a focused design note.
- Never include real cluster objects, credentials, provider payloads, customer
  names, webhook secrets, or Atlas connection strings in fixtures or logs.
- Follow the [Code of Conduct](CODE_OF_CONDUCT.md) and report vulnerabilities
  through [SECURITY.md](SECURITY.md), not a public issue.

## Local setup

The initial documentation baseline has no Go module or executable yet. For
current documentation-only work, clone the repository and use any available
Markdown checker:

```bash
git clone https://github.com/ddalcero/ruleraven.git
cd ruleraven
npx --yes markdownlint-cli2 '**/*.md'
```

Once the foundation milestone lands, the planned prerequisites are:

- the Go version pinned in `go.mod` and CI;
- Docker with the Compose/Testcontainers requirements supported;
- kubectl, Helm, and Kind for end-to-end work; and
- Git.

The planned setup and verification commands are:

```bash
go mod download
make test
make lint
```

Mongo integration tests will create a local single-node replica set through
Testcontainers. They must not use a contributor's production Atlas database.
Live provider and Atlas smoke tests will be opt-in and skipped unless their
explicit environment variables are present.

When these targets are introduced, `make help` and CI are the source of truth.
Do not add a documented command without adding it to the repository and
exercising it in CI.

## Test-driven development

Production behavior follows **RED → GREEN → REFACTOR**:

1. Add the smallest test that describes one missing behavior.
2. Run the focused test and confirm it fails for the intended reason.
3. Add the minimum implementation needed to pass.
4. Run the focused test again.
5. Refactor without changing behavior.
6. Run the full unit suite and the relevant integration or contract suite.
7. Commit a coherent, passing change.

For Go work, the target command sequence is:

```bash
go test -count=1 ./internal/package/...
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
```

Tests must be deterministic:

- inject clocks and randomness instead of sleeping;
- use `httptest.Server` for provider and notifier adapters;
- use a real Mongo replica set for transaction tests;
- use `envtest` for Kubernetes cache and lifecycle behavior;
- keep live network tests opt-in; and
- assert redaction and error behavior, not only happy paths.

A bug fix starts with a regression test. Security-sensitive paths require
negative tests: rejected Secret watches, forbidden redirects, oversized
responses, invalid structured output, unsafe database names, and RBAC denials.

## Commit and pull-request process

1. Branch from the latest `main`.
2. Make focused commits with imperative Conventional Commit-style subjects,
   for example `feat: add pod crash-loop rule` or
   `test: cover expired outbox leases`.
3. Update docs and examples in the same pull request as behavior changes.
4. Run every locally available check relevant to the change.
5. Open a pull request that explains:
   - the problem and scope;
   - the safety and data-handling impact;
   - the tests run and their results;
   - configuration, schema, or migration effects; and
   - any intentionally deferred work.
6. Address review comments with new commits or a clearly explained rebase. Do
   not force-push while a review is actively in progress without warning.

A pull request is ready to merge only when required CI passes, review concerns
are resolved, docs are accurate, and no unresolved security-sensitive behavior
is hidden behind a follow-up. Maintainers may squash on merge.

## Architecture guardrails

Contributions must preserve these boundaries unless an accepted design change
explicitly revises them:

- no automatic workload remediation;
- no Secret, pod-log, `pods/exec`, node, or wildcard RBAC access;
- no provider or notifier calls inside informer handlers;
- no raw Kubernetes-object persistence;
- no raw prompt/provider-response logging or persistence by default;
- deterministic critical rules cannot be downgraded by a provider;
- Mongo writes that create notification work remain transactional;
- notifications remain at-least-once and use stable event IDs; and
- notifier plugins are linked at compile time.

## Adding a decision provider

Provider adapters are planned under `internal/provider/<name>/` and implement
the common `provider.Provider` interface. When this package exists:

1. Add the adapter without changing the domain decision model.
2. Register an explicit provider type in `internal/provider/registry.go`.
3. Add strict configuration validation and environment-variable references;
   never accept credentials in the YAML body.
4. Translate the universal Noul, Choice, and Score questions into the provider's
   schema-constrained format.
5. Validate every returned question ID, type, choice, probability, and finite
   numeric value. Reject prose or partial output rather than guessing.
6. Classify retryable transport/status failures and honor the total deadline and
   response-size cap.
7. Populate audit fields such as requested/resolved model, request ID, latency,
   usage, attempts, and raw-response hash without storing the raw response.
8. Run the shared provider contract suite in
   `test/contract/provider_contract_test.go` against an `httptest.Server`.
9. Add adapter-specific fixtures, redaction tests, configuration docs, and an
   opt-in live smoke test if useful.

A provider returns typed evidence, not final severity or action. Provider SDKs
should be avoided when a small, auditable HTTP client is sufficient.

## Adding a notifier

Notifier adapters are planned under `internal/notify/<name>/` and implement the
common notifier and factory interfaces. When those packages exist:

1. Add a compile-time factory and register a stable notifier type.
2. Parse notifier-specific configuration strictly and resolve secrets by
   environment-variable name.
3. Keep incident policy out of the adapter; accept only the versioned message
   and destination supplied by the dispatcher.
4. Set explicit timeouts, request/response limits, redirect behavior, and TLS
   defaults. Treat destination URLs as startup configuration, never incident
   input.
5. Return classified errors with retryability and bounded `Retry-After` values.
6. Ensure retries preserve the same event ID and exact signed payload where the
   protocol requires it.
7. Add shared contract tests plus tests for credential redaction, status
   classification, cancellation, and oversized responses.
8. Document delivery semantics and consumer-side deduplication.

Hermes/Bastion integrations belong on the consumer side of the generic webhook;
do not add Hermes-specific business logic to RuleRaven.

## Documentation

Use descriptive headings, one sentence per line where practical, fenced code
blocks with language identifiers, and relative links for repository files.
Clearly distinguish current behavior from planned behavior while RuleRaven is in
Alpha. Run the repository's Markdown checks before opening a pull request.

## Code of conduct

Be respectful, specific, and patient. Critique code and decisions rather than
people. By participating, you agree to follow the
[Code of Conduct](CODE_OF_CONDUCT.md).
