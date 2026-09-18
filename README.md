# RuleRaven

> **Alpha — under active development.** RuleRaven is currently a design and
> planning project. The controller, container image, Helm chart, and commands
> described as "planned" below do not exist yet and must not be used in
> production.

RuleRaven is a planned, vigilant Kubernetes incident triage controller. It is
intended to turn noisy resource state and Kubernetes Events into stable,
explainable incident decisions without changing workloads.

## The problem

Kubernetes exposes useful failure signals, but operators must correlate Events,
conditions, owner relationships, and repeated reconciliations before deciding
whether a situation is actionable. Sending every signal directly to a person or
model creates duplicate alerts, leaks unnecessary cluster data, and makes
outcomes difficult to audit.

RuleRaven is being designed to:

- watch a deliberately small set of Kubernetes resources and Events;
- normalize and redact observations into bounded incident snapshots;
- resolve clear cases with deterministic, versioned rules;
- ask one configured decision provider only when a case is ambiguous;
- persist incidents, evaluations, and notification work atomically; and
- deliver versioned notifications that downstream systems can deduplicate.

## Planned architecture

```text
Kubernetes API (read-only watches)
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
       policy / decision composer
                  |
                  v
 MongoDB transaction: incident + evaluation + outbox
                  |
                  v
          outbox dispatcher
                  |
                  v
 optional compile-time notifier plugins
```

The implementation is planned in Go using
[controller-runtime](https://github.com/kubernetes-sigs/controller-runtime).
Event handlers will enqueue resource keys; provider and database calls will run
outside informer callbacks. Leader election will allow multiple replicas while
one Lease holder runs watches and workers.

The initial resource scope is planned to include Pods, Events, Deployments,
StatefulSets, DaemonSets, and Jobs. Initial deterministic rules will cover
crash loops, unschedulable Pods, failed Jobs, unavailable workloads, image-pull
failures, recovery, and selected ambiguous Warning Events.

See the [MVP implementation plan](docs/plans/2026-09-18-ruleraven-mvp.md) for
the intended package layout, TDD sequence, and acceptance criteria.

## Safety boundaries

Safety is an architectural constraint, not a provider prompt.

- **No automatic remediation.** RuleRaven may recommend an action but will not
  patch, delete, restart, scale, or execute commands in workloads.
- **Read-only workload access.** Planned Kubernetes permissions are `get`,
  `list`, and `watch` for explicitly supported resources.
- **Narrow writes.** Kubernetes writes are limited to leader-election Leases
  and, only when explicitly enabled, Kubernetes Events.
- **No Secret watches.** Configuration that attempts to watch Secrets will be
  rejected. The default RBAC will not permit Secret reads, pod execution, or
  workload mutation.
- **Allowlist normalization.** Snapshots will contain selected operational
  fields rather than complete Kubernetes objects. Managed fields, arbitrary
  annotations, environment values, command arguments, and Secret data are out
  of scope.
- **Deterministic authority.** A provider may escalate an ambiguous result but
  will not be allowed to downgrade a deterministic critical decision.
- **Bounded external calls.** Provider and webhook clients will enforce
  deadlines, response-size limits, retry classification, and redacted logs.
- **No exactly-once claim.** Notification delivery is planned as at-least-once;
  consumers must deduplicate by event ID.

## Provider-neutral decisions

RuleRaven will define a small internal decision interface based on typed
questions and answers. Providers return evidence — such as a choice, score, or
probability — rather than the final operational decision. A deterministic,
versioned composer will map validated answers and rule results to severity and
action.

Planned adapters are:

- TypeSafe direct Jev;
- OpenRouter Decisions/Jev;
- OpenAI structured outputs;
- Anthropic forced tool use; and
- generic OpenAI-compatible endpoints using strict JSON Schema or forced-tool
  mode.

One primary provider and, optionally, one ordered fallback will be configured.
Provider calls will be skipped for terminal deterministic rules. Raw prompts and
raw provider responses will not be stored by default. Live provider tests will
be opt-in and will not be required for ordinary pull requests.

## MongoDB Atlas

The planned persistence layer uses a **new, explicitly named MongoDB Atlas
database** for each deployment or cluster. RuleRaven will never rely on an
implicit database from the connection URI, and it will reject the administrative
names `admin`, `local`, and `config`.

Planned collections are:

- `incidents` — current lifecycle and latest decision references;
- `snapshots` — immutable, normalized observations;
- `evaluations` — deterministic and provider audit records; and
- `notification_outbox` — leased, retryable delivery work.

Unique keys will make repeated reconciliations idempotent. A MongoDB transaction
will commit an incident transition, evaluation, and outbox entries together.
TTL indexes will bound retention, and startup index verification will block
readiness when idempotency constraints are uncertain. Integration tests will use
a replica set so transaction behavior is exercised rather than mocked.

## Optional integrations

Notifications are planned as **optional, compile-time Go plugins**, not runtime
`.so` modules. The first notifier will be a generic HTTPS webhook with a
versioned CloudEvents-style envelope and an HMAC-SHA256 signature over the exact
request body.

Hermes/Bastion is only an external webhook consumer. RuleRaven will contain no
Hermes-specific triage logic, credentials, or control path. Other notifiers can
be added behind the same factory and contract-test boundaries without changing
incident policy.

## Planned quick start

> These commands describe the target developer experience. They are not
> expected to work until the corresponding MVP milestones are implemented.

Prerequisites are expected to be Go, Docker, kubectl, Helm, Kind, and access to
a dedicated MongoDB Atlas test database (or a local replica set for tests).

```bash
git clone https://github.com/ddalcero/ruleraven.git
cd ruleraven
cp config/example.yaml config/local.yaml
export MONGODB_URI='mongodb+srv://...'
# Export exactly one configured provider credential and optional webhook values.
make test
make kind-up
make e2e
```

The planned cluster installation flow is:

```bash
helm upgrade --install ruleraven deploy/helm/ruleraven \
  --namespace ruleraven-system \
  --create-namespace \
  --values config/your-values.yaml
```

Until the chart and release process exist, do not copy these commands into an
operations runbook. Track executable setup instructions in the roadmap and
release notes.

## Project status

**Alpha / under active development.** The repository currently establishes the
architecture, contribution policy, security policy, and implementation plan.
There is no production code, released binary, container image, supported Helm
chart, compatibility guarantee, or stable configuration/API contract yet.

Early contributors should expect package names, schemas, configuration, and
interfaces to change. Design discussion and test-first implementation pull
requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md).

## Roadmap

### Foundation

- [ ] Bootstrap the Go module, health server, configuration, and CI.
- [ ] Define domain types, canonical snapshots, redaction, and fingerprints.
- [ ] Add deterministic rules with threshold and recovery tests.

### Decisions and persistence

- [ ] Implement the universal typed-question contract and decision composer.
- [ ] Add provider adapters and shared contract tests.
- [ ] Implement Atlas persistence, transactions, indexes, and outbox leasing.

### Delivery and control plane

- [ ] Add the signed generic webhook and dispatcher.
- [ ] Build the controller pipeline, resolution sweeper, telemetry, and probes.
- [ ] Add a hardened container image and least-privilege Helm chart.

### Verification and release

- [ ] Exercise the system end to end with Kind, a Mongo replica set, provider
      stub, and webhook receiver.
- [ ] Prove RBAC denials, idempotency, failover, and redaction.
- [ ] Publish signed multi-architecture images, an SBOM, provenance, and a Helm
      chart only after all MVP acceptance criteria pass.

## Contributing and security

Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request. Community
participation is governed by the [Code of Conduct](CODE_OF_CONDUCT.md). Report
security issues privately as described in [SECURITY.md](SECURITY.md); do not
open a public issue for a suspected vulnerability.

## License

RuleRaven is licensed under the [MIT License](LICENSE).
