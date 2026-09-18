# RuleRaven

> **Alpha — under active development.** RuleRaven has a working controller,
> container build, and Helm chart, but no stable release or production-support
> guarantee. Configuration, schemas, and behavior may change without notice.

RuleRaven is a read-only Kubernetes incident triage controller. It turns a
bounded set of workload state and Kubernetes Events into stable, explainable
incident decisions without changing workloads.

## What works in Alpha

The current implementation:

- watches Pods, Events, Deployments, StatefulSets, DaemonSets, and Jobs;
- normalizes and redacts observations into bounded snapshots with stable
  fingerprints;
- evaluates crash loops, scheduling failures, image-pull failures, failed Jobs,
  unavailable workloads, Warning Events, and recovery with deterministic rules;
- calls a configured decision provider only for ambiguous cases;
- validates provider output before a deterministic composer assigns severity and
  action;
- stores incidents, immutable snapshots, evaluations, and delivery work in
  MongoDB with transactional outbox semantics;
- sends at-least-once, HMAC-signed, CloudEvents-style webhook notifications;
- exposes `/healthz`, `/readyz`, and Prometheus-format `/metrics`; and
- ships a non-root distroless image build and a least-privilege Helm chart.

The production provider registry covers direct TypeSafe/Jev, OpenRouter
Decisions, OpenAI structured output, Anthropic forced-tool output, and a strict
generic `openai-compatible` adapter. Generic endpoints must use HTTPS and
explicitly select `json_schema` or `forced_tool`; native providers reject
endpoint overrides and unstructured text modes are rejected.

An Alpha EKS smoke deployment has exercised health and readiness, metrics, a
live Jev decision, MongoDB indexes and transactions, outbox dispatch, and a
signed generic webhook. That smoke test is evidence of integration, not a
production-readiness claim.

## Architecture

```text
Kubernetes API (allowlisted read-only watches)
              |
              v
      normalizer + redactor
              |
              v
    stable snapshot + fingerprint
              |
              v
       deterministic rules
          /             \
 terminal decision    ambiguous
          |              |
          |              v
          |       decision provider
          |              |
          +-------+------+
                  v
       deterministic decision composer
                  |
                  v
 MongoDB transaction: incident + evaluation + outbox
                  |
                  v
          outbox dispatcher
                  |
                  v
       signed generic HTTPS webhook
```

Informer handlers enqueue resource keys; provider and database work runs in
workers. Reconciliation is idempotent, and stable event IDs let webhook
consumers deduplicate at-least-once delivery. Startup readiness requires valid
configuration, verified MongoDB indexes, and synchronized Kubernetes informers.

## Safety boundaries

Safety is enforced in configuration, normalization, RBAC, and adapters rather
than delegated to a model prompt.

- **No automatic remediation.** RuleRaven does not patch, delete, restart,
  scale, or execute commands in workloads.
- **Read-only workload access.** Chart RBAC grants `get`, `list`, and `watch`
  only for supported resources. Optional Event emission adds only `create` and
  `patch` on Events.
- **No Secret watches.** Configuration rejects Secret resources. The chart does
  not grant Secret reads, pod logs, `pods/exec`, node, or wildcard access.
- **Allowlist normalization.** Snapshots contain selected operational fields,
  not complete Kubernetes objects, arbitrary annotations, environment values,
  command arguments, or Secret data.
- **Deterministic authority.** Provider evidence cannot downgrade a
  deterministic critical result.
- **Bounded external calls.** Provider and webhook clients enforce timeouts,
  response limits, retry classification, redacted logs, and redirect controls.
- **At-least-once notifications.** Consumers must verify signatures, enforce a
  timestamp window, and deduplicate by event ID.

## Quick start

### Developer checks

Prerequisites are Go 1.22.2 or a compatible Go 1.22 toolchain, Docker, Helm 3,
Python 3 with PyYAML for chart assertions, and Node.js for Markdown linting.

```bash
git clone https://github.com/ddalcero/ruleraven.git
cd ruleraven
go mod download
make check
make integration
make helm-test
make docker-build
```

`make integration` starts a disposable MongoDB 7 single-node replica set with
Testcontainers. It requires a working Docker daemon and never needs an Atlas
credential.

### Run from source

Copy the example, select a provider, and set only environment-variable
references in YAML. Never place credentials or a MongoDB URI in the config
file.

```bash
cp config/example.yaml config/local.yaml
export MONGODB_URI='mongodb://localhost:27017/?replicaSet=rs0'
export PROVIDER_KEY='<provider-api-key>'
export FALLBACK_KEY='<fallback-api-key>'
export WEBHOOK_SECRET='<random-shared-secret>'
export KUBECONFIG="$HOME/.kube/config"
go run ./cmd/ruleraven --config config/local.yaml
```

The example enables a webhook and fallback provider. Remove those sections if
not needed. MongoDB must support transactions; a standalone `mongod` is not
sufficient.

### Install the chart

Create the referenced Kubernetes Secret separately, keep secrets out of values
files, and start with one namespace and one replica. Then install an immutable
Alpha image tag:

```bash
helm lint --strict deploy/helm/ruleraven
helm upgrade --install ruleraven deploy/helm/ruleraven \
  --namespace ruleraven-system \
  --create-namespace \
  --values /path/to/non-secret-values.yaml \
  --set-string image.tag='<immutable-alpha-tag>'
```

Follow the
[test deployment runbook](docs/operations/test-deployment.md) for Secret keys,
provider choices, RBAC checks, Atlas requirements, verification, and cleanup.

## Known limitations

- RuleRaven remains Alpha. There is no stable API/configuration contract,
  compatibility promise, signed release image, SBOM, or production support.
- Automatic remediation is intentionally absent; output is advisory and
  notification-only.
- Leader election is not implemented. The chart enforces one replica and uses a
  `Recreate` rollout so upgrades cannot overlap controllers.
- MongoDB transactions require a replica set. The current live smoke uses an
  ephemeral, single-node in-cluster replica set because the available Atlas API
  keys and database users could not provision the isolated Atlas test database.
- Atlas provisioning is external to RuleRaven and requires appropriate Atlas
  project permissions plus a database user authorized for the explicitly named
  RuleRaven database.
- Cluster admission or external webhook authorizers can grant a service account
  more effective access than the chart's RBAC. Verify effective permissions in
  every target cluster instead of treating rendered RBAC as the whole policy.
- A quick Cloudflare tunnel can be useful for a short webhook smoke test, but it
  is temporary test infrastructure and is not a supported deployment endpoint.

## Collaboration

RuleRaven welcomes focused issues and pull requests for reproducible failures,
safety improvements, provider/notifier adapters, rules, tests, and operations
documentation. Please:

1. search existing issues and pull requests;
2. describe the failure mode, expected result, and safety or data impact;
3. keep changes narrow and add regression or contract tests;
4. run the checks in [CONTRIBUTING.md](CONTRIBUTING.md); and
5. avoid real cluster data, URLs, account names, credentials, and provider
   payloads in issues, fixtures, logs, and commits.

Use GitHub private vulnerability reporting for security issues as described in
[SECURITY.md](SECURITY.md). Community participation follows the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Project status

**Alpha / working prototype.** The repository contains the controller,
provider-neutral decision contracts, provider adapters, MongoDB transactional
persistence, signed webhooks, telemetry, Docker packaging, Helm deployment, and
unit, contract, integration, and chart tests. The next work is hardening,
leader election, broader deployment testing, release provenance, and a stable
configuration/release policy.

See [CHANGELOG.md](CHANGELOG.md) for notable changes and the
[MVP implementation plan](docs/plans/2026-09-18-ruleraven-mvp.md) for design
history. The plan is historical context where it conflicts with working code.

## License

RuleRaven is licensed under the [MIT License](LICENSE).
