# RuleRaven MVP Implementation Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task.

**Goal:** Build an Alpha Kubernetes incident triage controller that creates
stable, redacted incidents, resolves clear cases with deterministic rules, uses
a provider-neutral decision interface for ambiguity, and sends optional signed
notifications without remediating workloads.

**Architecture:** A leader-elected Go controller watches an explicit read-only
resource set, queues keys, normalizes observations, and evaluates deterministic
rules before any model call. A versioned composer combines rule results with
validated provider evidence, then a MongoDB Atlas transaction commits the
incident, evaluation, and notification outbox; compile-time notifier plugins
deliver at-least-once events outside reconciliation.

**Tech Stack:** Go, controller-runtime/client-go, MongoDB Go driver and Atlas,
Prometheus client, Testcontainers, envtest, Kind, Helm, GitHub Actions,
CloudEvents-style JSON, and HMAC-SHA256.

> **Status:** This is an implementation plan for an Alpha project under active
> development. At the time of writing, only documentation and the MIT license
> exist. Every binary, command, API, chart, and behavior below is planned, not a
> claim about current functionality.

---

## Invariants and definition of done

These rules apply to every task:

1. Write one failing behavior test and observe the intended failure before
   writing production code.
2. Add only enough production code to pass, then refactor while tests remain
   green.
3. Run the focused test first and `go test -count=1 ./...` before each code
   commit. Do not hide flaky behavior with retries.
4. Inject clocks, ID generators, HTTP clients, and jitter. Unit tests must not
   sleep or use live services.
5. Never log or persist credentials, raw provider bodies, raw prompts, complete
   Kubernetes objects, environment values, arbitrary command arguments, or
   unallowlisted annotations.
6. RuleRaven never mutates workloads. Kubernetes writes are limited to
   leader-election Leases and optional Event creation/patching.
7. A deterministic critical decision cannot be downgraded by provider output.
8. Delivery is at-least-once. Stable event IDs and consumer deduplication are
   part of the contract; do not claim exactly-once behavior.
9. Make one focused commit after each task. If a task is split among
   contributors, preserve RED/GREEN history in separate reviewable commits.

Use the module path `github.com/ddalcero/ruleraven`. Use `internal/` deliberately:
the MVP exposes an executable and versioned webhook envelope, not a stable Go
SDK.

## Target layout

```text
cmd/ruleraven/main.go
cmd/smoke-webhook/main.go
config/example.yaml
internal/app/
internal/config/
internal/controller/
internal/decision/
internal/domain/
internal/health/
internal/kube/
internal/notify/
internal/provider/
internal/rules/
internal/store/mongo/
internal/telemetry/
test/contract/
test/e2e/
test/integration/
deploy/docker/Dockerfile
deploy/helm/ruleraven/
.github/workflows/
```

## Planned incident flow

1. An informer handler receives an add, update, or delete signal and enqueues a
   stable resource key; it performs no provider or MongoDB I/O.
2. A worker resolves the current object and related owner/Event facts.
3. The normalizer allowlists fields, canonicalizes order, redacts unsafe data,
   and computes an incident key and content hash without volatile metadata.
4. Rules return `terminal`, `semantic`, `suppress`, or `no_match`.
5. Semantic results call the configured primary provider and optional ordered
   fallback. Validated typed answers feed the deterministic composer.
6. One Mongo transaction updates the incident and inserts an immutable
   evaluation plus one outbox row per selected destination.
7. Independent dispatcher workers lease outbox rows and invoke compile-time
   notifier plugins.
8. Recovery, disappearance, or an Event quiet period moves an incident to
   `resolved` and creates exactly one resolution notification.

---

### Task 1: Bootstrap the Go module and process health server

**Objective:** Create a buildable binary with deterministic health behavior and
graceful shutdown, without connecting to Kubernetes, MongoDB, or providers.

**Files:**

- Create: `go.mod`
- Create: `cmd/ruleraven/main.go`
- Create: `internal/health/server.go`
- Create: `internal/health/server_test.go`
- Create: `Makefile`

#### Task 1, step 1: Write the failing health test

Add a test using `httptest.NewRecorder` that calls a `health.Handler` and asserts
`GET /healthz` returns status 200 and body `ok\n`; assert unknown paths return
404 and non-GET requests return 405.

```go
func TestHandlerHealthz(t *testing.T) {
    req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
    rec := httptest.NewRecorder()
    health.NewHandler().ServeHTTP(rec, req)
    require.Equal(t, http.StatusOK, rec.Code)
    require.Equal(t, "ok\n", rec.Body.String())
}
```

#### Task 1, step 2: Verify RED

Run:

```bash
go test -count=1 ./internal/health
```

Expected: FAIL because `health.NewHandler` does not exist.

#### Task 1, step 3: Implement the minimum server

Initialize `go.mod` with the public module path. Implement an explicit
`http.ServeMux`, a server with injected address and timeouts, and a `Run(ctx)`
method that treats `http.ErrServerClosed` as a clean shutdown. In `main`, create
a signal-aware context and return a nonzero exit status only on a real startup or
shutdown error. Add `make test`, `make test-race`, `make vet`, and `make build`
targets.

#### Task 1, step 4: Verify GREEN

Run:

```bash
go test -count=1 ./internal/health
go test -count=1 ./...
go vet ./...
go build ./cmd/ruleraven
```

Expected: all commands exit 0 and produce no failing tests.

#### Task 1, step 5: Commit

```bash
git add go.mod go.sum cmd/ruleraven internal/health Makefile
git commit -m "build: bootstrap RuleRaven controller"
```

---

### Task 2: Define strict typed configuration

**Objective:** Load one versioned YAML document, resolve credentials only by
environment-variable name, and reject unsafe or inconsistent configuration.

**Files:**

- Create: `internal/config/config.go`
- Create: `internal/config/load.go`
- Create: `internal/config/validate.go`
- Create: `internal/config/config_test.go`
- Create: `config/example.yaml`
- Modify: `go.mod`

#### Task 2, step 1: Write failing table tests

Cover one valid example and at least these rejected cases:

- unknown YAML fields or unsupported config version;
- provider/notifier type not in its compile-time registry;
- missing referenced environment variable;
- database `admin`, `local`, or `config`, or an empty explicit database;
- a watched Secret;
- duplicate destination IDs;
- HTTP webhook without `allowInsecureHTTP: true`;
- fallback provider equal to primary;
- nonpositive timeouts/state limits and invalid retry ranges; and
- cluster-wide RBAC combined with an inconsistent namespace-only setting.

Use an injected `LookupEnv func(string) (string, bool)` rather than mutating the
real process environment in parallel tests.

#### Task 2, step 2: Verify RED

Run:

```bash
go test -count=1 ./internal/config -run 'TestLoad|TestValidate'
```

Expected: FAIL because strict loading and validation are absent.

#### Task 2, step 3: Implement minimum configuration behavior

Use `yaml.Decoder.KnownFields(true)`. Define typed sections for cluster,
controller, Mongo, rules, decision providers, notifications, HTTP, and logging.
Apply documented defaults before validation, but never default a database name or
credential. Keep provider and notifier type allowlists injected so config does
not import concrete adapters. The example must use placeholders such as
`MONGODB_URI`, never a credential value.

#### Task 2, step 4: Verify GREEN and example parity

Run:

```bash
go test -count=1 ./internal/config
go test -count=1 ./...
```

Expected: PASS, including a test that loads `config/example.yaml` with a fake
environment map.

#### Task 2, step 5: Commit

```bash
git add go.mod go.sum internal/config config/example.yaml
git commit -m "feat: add strict controller configuration"
```

---

### Task 3: Model incidents and canonical snapshot fingerprints

**Objective:** Define stable domain types and hashes that ignore Kubernetes
churn while changing for operationally material facts.

**Files:**

- Create: `internal/domain/source.go`
- Create: `internal/domain/snapshot.go`
- Create: `internal/domain/incident.go`
- Create: `internal/domain/decision.go`
- Create: `internal/domain/evaluation.go`
- Create: `internal/domain/notification.go`
- Create: `internal/kube/fingerprint.go`
- Create: `internal/kube/fingerprint_test.go`

#### Task 3, step 1: Write failing equivalence tests

Build two snapshots with reordered labels, conditions, and containers and with
different `ObservedAt`, `ResourceVersion`, and Event count. Assert identical
content hashes. Then vary a condition status, waiting reason, generation, or
owner UID and assert a different content hash. Assert the incident key includes
cluster ID and source UID, not a mutable display name alone.

#### Task 3, step 2: Verify RED

Run:

```bash
go test -count=1 ./internal/kube -run 'TestContentHash|TestIncidentKey'
```

Expected: FAIL because the fingerprinter does not exist.

#### Task 3, step 3: Implement canonical encoding

Define explicit enums:

```go
type Severity string // info, warning, critical
type Action string   // record, notify, page, suppress
type IncidentStatus string // open, resolved, suppressed
```

Canonicalize slices and maps into dedicated hash DTOs; never hash a serialized
`runtime.Object`. Use SHA-256 with a schema/version prefix. Exclude observed
timestamps and event repetition count unless a later rule explicitly models a
material duration boundary.

#### Task 3, step 4: Verify GREEN

Run:

```bash
go test -count=1 ./internal/kube
go test -count=1 ./...
```

Expected: PASS with deterministic hashes across 100 randomized reorderings.

#### Task 3, step 5: Commit

```bash
git add internal/domain internal/kube/fingerprint.go internal/kube/fingerprint_test.go
git commit -m "feat: define incident domain and stable fingerprints"
```

---

### Task 4: Normalize and redact supported Kubernetes resources

**Objective:** Convert supported resources into bounded allowlisted snapshots
without exposing complete objects or sensitive fields.

**Files:**

- Create: `internal/kube/normalizer.go`
- Create: `internal/kube/redactor.go`
- Create: `internal/kube/normalizer_test.go`
- Create: `internal/kube/redactor_test.go`
- Create: `test/testdata/kube/pod-crashloop.yaml`
- Create: `test/testdata/kube/pod-unschedulable.yaml`
- Create: `test/testdata/kube/job-failed.yaml`
- Create: `test/testdata/kube/workloads.yaml`
- Create: `test/testdata/kube/warning-event.yaml`

#### Task 4, step 1: Add one failing Pod normalization test

Assert source/owner identity, sorted conditions, selected container state and
restart facts, and allowlisted labels. Seed the fixture with managed fields,
Secret references, environment values, arbitrary annotations, command/args, and
a token-like string; assert none appears in marshaled snapshot JSON.

#### Task 4, step 2: Verify RED

Run:

```bash
go test -count=1 ./internal/kube -run TestNormalizePod
```

Expected: FAIL because no normalizer exists.

#### Task 4, step 3: Implement Pod normalization and repeat by kind

Implement only allowlisted fields for Pods, then repeat a RED/GREEN cycle for
Deployment, StatefulSet, DaemonSet, Job, and Event. Reject Secret objects even if
called directly. Generic unstructured resources may include identity and
allowlisted conditions only; they must not recursively copy `spec` or `status`.
Enforce the configured maximum normalized state size before provider use.

#### Task 4, step 4: Add redaction regression tests

Fuzz or table-test keys containing `secret`, `token`, `password`, `authorization`,
and `credential`. Test that error strings and structured logs cannot include the
unsafe fixture values.

#### Task 4, step 5: Verify GREEN

Run:

```bash
go test -count=1 ./internal/kube
go test -count=1 ./...
```

Expected: PASS for every supported kind and leak sentinel.

#### Task 4, step 6: Commit

```bash
git add internal/kube test/testdata/kube go.mod go.sum
git commit -m "feat: normalize and redact Kubernetes observations"
```

---

### Task 5: Implement deterministic triage rules

**Objective:** Handle explainable failures and recovery without calling a model.

**Files:**

- Create: `internal/rules/engine.go`
- Create: `internal/rules/pod.go`
- Create: `internal/rules/workload.go`
- Create: `internal/rules/job.go`
- Create: `internal/rules/event.go`
- Create: `internal/rules/recovery.go`
- Create: `internal/rules/engine_test.go`
- Create: `internal/rules/pod_test.go`
- Create: `internal/rules/workload_test.go`
- Create: `internal/rules/job_test.go`
- Create: `internal/rules/event_test.go`

#### Task 5, step 1: Define the result contract in a failing test

```go
type Disposition string // terminal, semantic, suppress, no_match

type Result struct {
    Matches       []Match
    Disposition   Disposition
    Decision      *domain.Decision
    QuestionSet   string
    PolicyVersion string
}
```

Assert a Pod below the restart/age threshold is `no_match`, at the threshold is
`terminal`, and beyond the critical threshold is critical.

#### Task 5, step 2: Verify RED

Run:

```bash
go test -count=1 ./internal/rules -run TestCrashLoopThresholds
```

Expected: FAIL because the rule engine is absent.

#### Task 5, step 3: Implement each rule in its own RED/GREEN cycle

Implement, in order:

1. `pod-crash-loop`;
2. `pod-unschedulable`;
3. `image-pull-failure`;
4. `job-failed`;
5. `workload-unavailable`;
6. selected `warning-event` returning `semantic`; and
7. `recovered` when a previously open incident no longer matches.

For every rule, test just below, exactly at, and just above time/count thresholds.
Sort matches by stable rule ID before composition. Ignored namespaces/label
selectors return `suppress` with a recorded reason.

#### Task 5, step 4: Add precedence tests

Assert deterministic critical matches win over lower-severity matches and expose
a non-downgrade floor to the composer. Equivalent repeated Events must not
create a new decision solely because Event count or timestamp changed.

#### Task 5, step 5: Verify GREEN

Run:

```bash
go test -count=1 ./internal/rules
go test -count=1 ./...
```

Expected: PASS with no network or database dependencies.

#### Task 5, step 6: Commit

```bash
git add internal/rules
git commit -m "feat: add deterministic incident rules"
```

---

### Task 6: Define typed questions, answer validation, and composition

**Objective:** Create the provider-neutral evidence contract and deterministic
mapping from valid evidence to decisions.

**Files:**

- Create: `internal/provider/provider.go`
- Create: `internal/provider/validate.go`
- Create: `internal/provider/validate_test.go`
- Create: `internal/decision/questions.go`
- Create: `internal/decision/schema.go`
- Create: `internal/decision/composer.go`
- Create: `internal/decision/composer_test.go`
- Create: `internal/decision/testdata/answer-schema.golden.json`

#### Task 6, step 1: Write failing validation tests

Model `noul`, `choice`, and `score` questions. Test valid answers and reject:
missing/extra IDs, wrong type, unknown choices, probabilities outside `[0,1]`,
NaN/infinite values, duplicate legend values, and confidence on a Noul answer.

#### Task 6, step 2: Verify RED

Run:

```bash
go test -count=1 ./internal/provider -run TestValidateAnswers
```

Expected: FAIL because validation is absent.

#### Task 6, step 3: Implement the interface and validator

```go
type Provider interface {
    Name() string
    Evaluate(context.Context, EvaluationRequest) (EvaluationResponse, error)
}
```

Include state JSON, question map, rubric version, and request ID in requests.
Responses include answers, provider, requested/resolved model, request ID, usage,
latency, and only a hash of the raw response. Generate one strict JSON Schema
from the same question definitions used by validation; golden-test it.

#### Task 6, step 4: Write failing composer tests and implement

Add the `operational-triage-v1` questions: incident family, operational impact,
requires immediate attention, and likely transient. Test versioned thresholds,
provider-unavailable fallback (`warning/notify` by default), and the invariant
that provider evidence cannot lower a deterministic critical floor.

#### Task 6, step 5: Verify GREEN

Run:

```bash
go test -count=1 ./internal/provider ./internal/decision
go test -count=1 ./...
```

Expected: PASS and stable schema golden output.

#### Task 6, step 6: Commit

```bash
git add internal/provider/provider.go internal/provider/validate.go \
  internal/provider/validate_test.go internal/decision
git commit -m "feat: add provider-neutral decision contract"
```

---

### Task 7: Build shared provider retry and contract infrastructure

**Objective:** Give every adapter the same cancellation, retry, response-limit,
validation, and credential-redaction behavior.

**Files:**

- Create: `internal/provider/retry.go`
- Create: `internal/provider/retry_test.go`
- Create: `internal/provider/httpclient.go`
- Create: `internal/provider/registry.go`
- Create: `test/contract/provider_contract.go`
- Create: `test/contract/provider_contract_test.go`

#### Task 7, step 1: Write failing retry tests with fake time

Classify transport failures and statuses `408`, `429`, `500`, `502`, `503`,
`524`, and `529` as retryable. Authentication, authorization, payment,
malformed request, unsupported model, and payload-too-large errors are permanent.
Test capped exponential backoff, injected jitter, bounded `Retry-After`, context
cancellation, total deadline, and maximum attempts.

#### Task 7, step 2: Verify RED

Run:

```bash
go test -count=1 ./internal/provider -run 'TestRetry|TestStatusClassification'
```

Expected: FAIL because retry policy is absent.

#### Task 7, step 3: Implement shared behavior

Use an injected sleeper and random source. Bound request and response bodies.
Return typed errors without response bodies or authorization headers. Register
factories by explicit type and reject duplicates.

#### Task 7, step 4: Create the reusable contract suite

The suite must exercise valid Noul/Choice/Score responses, missing and unknown
answers, invalid numeric values, prose around JSON, wrong tool names, retryable
and permanent responses, `Retry-After`, cancellation, response-size limits, and
credential leak sentinels. It accepts an adapter factory and fake server so each
adapter executes identical assertions.

#### Task 7, step 5: Verify GREEN

Run:

```bash
go test -count=1 ./internal/provider ./test/contract
go test -race -count=1 ./internal/provider ./test/contract
```

Expected: PASS without live credentials.

#### Task 7, step 6: Commit

```bash
git add internal/provider test/contract
git commit -m "test: define decision provider contract"
```

---

### Task 8: Add TypeSafe and OpenRouter Decisions adapters

**Objective:** Translate the universal question map to calibrated Jev decision
APIs without changing domain policy.

**Files:**

- Create: `internal/provider/typesafe/client.go`
- Create: `internal/provider/typesafe/client_test.go`
- Create: `internal/provider/openrouter/client.go`
- Create: `internal/provider/openrouter/client_test.go`
- Modify: `internal/provider/registry.go`
- Create: `test/testdata/providers/typesafe/*.json`
- Create: `test/testdata/providers/openrouter/*.json`

#### Task 8, step 1: RED/GREEN the TypeSafe request

Against `httptest.Server`, assert the adapter sends normalized JSON as `state`,
the universal question map, configured Jev model, bearer credential, request
ID, and no unsupported fields. Verify credential absence from returned errors.

#### Task 8, step 2: RED/GREEN the TypeSafe response

Parse Noul, Choice, and Score results; validate answer IDs and criteria; record
the resolved model, usage, latency, request ID, attempts, and raw hash. Store no
raw response.

#### Task 8, step 3: RED/GREEN OpenRouter Decisions

Target `/api/alpha/decisions`, preserve compatible instructions/criteria, and
capture the actual resolved model rather than assuming an alias. Parse optional
confidence/distribution fields defensively and reject schema violations.

#### Task 8, step 4: Run shared contracts

```bash
go test -count=1 ./internal/provider/typesafe ./internal/provider/openrouter
go test -count=1 ./test/contract -run 'TestProviderContract/(typesafe|openrouter)'
go test -count=1 ./...
```

Expected: PASS using only local fake servers.

#### Task 8, step 5: Commit

```bash
git add internal/provider/typesafe internal/provider/openrouter \
  internal/provider/registry.go test/testdata/providers test/contract
git commit -m "feat: add Jev decision provider adapters"
```

---

### Task 9: Add OpenAI, Anthropic, and generic compatible adapters

**Objective:** Support strict structured output across common generative APIs
without accepting free-form model text.

**Files:**

- Create: `internal/provider/openai/client.go`
- Create: `internal/provider/openai/client_test.go`
- Create: `internal/provider/anthropic/client.go`
- Create: `internal/provider/anthropic/client_test.go`
- Create: `internal/provider/openaicompat/client.go`
- Create: `internal/provider/openaicompat/client_test.go`
- Modify: `internal/provider/registry.go`
- Create: `test/testdata/providers/openai/*.json`
- Create: `test/testdata/providers/anthropic/*.json`

#### Task 9, step 1: RED/GREEN OpenAI structured output

Assert strict `response_format` JSON Schema, required fields, no additional
properties, configured model, and bounded state. Reject omitted fields, prose,
unknown answer IDs, and schema-invalid numbers. Do not silently retry in plain
JSON/text mode.

#### Task 9, step 2: RED/GREEN Anthropic forced tool use

Define exactly one answer-submission tool with the generated schema and force
that tool. Accept only its tool input. Reject missing, wrong, or multiple
conflicting invocations.

#### Task 9, step 3: RED/GREEN generic OpenAI-compatible modes

Support only explicit `json_schema` and `forced_tool` modes. Keep JSON-mode
fallback disabled by default and test that an unsupported strict mode fails
clearly rather than degrading.

#### Task 9, step 4: Run all contracts

```bash
go test -count=1 ./internal/provider/openai \
  ./internal/provider/anthropic ./internal/provider/openaicompat
go test -count=1 ./test/contract -run TestProviderContract
go test -count=1 ./...
```

Expected: PASS with no outbound network calls.

#### Task 9, step 5: Commit

```bash
git add internal/provider/openai internal/provider/anthropic \
  internal/provider/openaicompat internal/provider/registry.go \
  test/testdata/providers test/contract
git commit -m "feat: add structured-output provider adapters"
```

---

### Task 10: Implement MongoDB Atlas persistence and indexes

**Objective:** Make incident updates idempotent and atomically couple decisions
with notification work in an explicitly named database.

**Files:**

- Create: `internal/store/store.go`
- Create: `internal/store/mongo/client.go`
- Create: `internal/store/mongo/indexes.go`
- Create: `internal/store/mongo/incidents.go`
- Create: `internal/store/mongo/evaluations.go`
- Create: `internal/store/mongo/outbox.go`
- Create: `internal/store/mongo/mapping.go`
- Create: `test/integration/mongo_test.go`
- Create: `test/integration/pipeline_test.go`

#### Task 10, step 1: Start with a failing replica-set integration test

Use Testcontainers to start a single-node Mongo replica set. Assert startup
creates these collections and named indexes:

- unique incidents on `{cluster_id, incident_key}`;
- unique snapshots on `{incident_id, content_hash}`;
- unique evaluations on incident, snapshot hash, policy, rubric, and provider
  config hash;
- unique outbox entries on evaluation, destination, and event type;
- query/lease indexes; and
- single-field TTL indexes on `expires_at`.

Assert administrative database names are rejected before a connection attempt.

#### Task 10, step 2: Verify RED

Run:

```bash
go test -count=1 -tags=integration ./test/integration -run TestMongoIndexes
```

Expected: FAIL because the store and indexes do not exist.

#### Task 10, step 3: Implement indexes and readiness checks

Use one explicit database from config. Reconcile exact names, key patterns,
uniqueness, and TTL options. An incompatible existing index is a readiness error;
do not silently drop it. Keep Atlas retryable writes enabled.

#### Task 10, step 4: RED/GREEN each write operation

In separate test cycles implement:

1. immutable, duplicate-safe snapshot upsert;
2. optimistic incident update with a version predicate;
3. transactionally update incident + insert evaluation + insert outbox;
4. atomically claim a pending/expired delivery with worker and lease; and
5. complete or reschedule delivery with classified status.

Test transaction rollback, 20 concurrent equivalent upserts, two concurrent
claimers, and lease recovery after a simulated worker crash.

#### Task 10, step 5: Verify GREEN

Run:

```bash
go test -count=1 ./internal/store/...
go test -count=1 -tags=integration ./test/integration/...
go test -race -count=1 ./internal/store/...
go test -count=1 ./...
```

Expected: PASS; transaction tests must prove the replica set is active rather
than skipping.

#### Task 10, step 6: Commit

```bash
git add internal/store test/integration go.mod go.sum
git commit -m "feat: persist incidents with transactional outbox"
```

---

### Task 11: Implement signed webhook notifications and outbox dispatch

**Objective:** Deliver versioned, authenticated events through compile-time
plugins with bounded at-least-once retry behavior.

**Files:**

- Create: `internal/notify/notifier.go`
- Create: `internal/notify/registry.go`
- Create: `internal/notify/signing.go`
- Create: `internal/notify/signing_test.go`
- Create: `internal/notify/dispatcher.go`
- Create: `internal/notify/dispatcher_test.go`
- Create: `internal/notify/webhook/webhook.go`
- Create: `internal/notify/webhook/webhook_test.go`
- Create: `test/contract/notifier_contract_test.go`
- Create: `cmd/smoke-webhook/main.go`

#### Task 11, step 1: Write the failing signature test

For fixed timestamp and raw body, assert lowercase hex HMAC-SHA256 over
`<unix-seconds>.<raw-body>` and constant-time verification. Assert changing one
body byte fails verification.

#### Task 11, step 2: Verify RED

Run:

```bash
go test -count=1 ./internal/notify -run TestSignature
```

Expected: FAIL because signing is absent.

#### Task 11, step 3: Implement webhook delivery

Send `application/cloudevents+json` with stable event ID, timestamp, and
`v1=<hex>` signature headers. Require HTTPS by default, disable redirects, block
loopback/link-local destinations unless explicitly allowed for tests, enforce
attempt timeout and bounded response reads, and never log the secret.

Classify `2xx` as success; `408`, `409`, `425`, `429`, and `5xx` as retryable;
other `4xx` as permanent. Bound `Retry-After` by config.

#### Task 11, step 4: RED/GREEN the dispatcher

Use the store lease API, injected clock, and notifier registry. Test completion,
retry scheduling, max-attempt exhaustion, expired lease recovery, cancellation,
and that retries retain the event ID. One destination must not block others.

#### Task 11, step 5: Add contract and smoke receiver

Contract-test type registration, strict config, classified errors, redaction,
and cancellation. The smoke receiver verifies signature timestamp window and
deduplicates event IDs; it is a test utility, not a production integration.
Hermes/Bastion remains an external generic-webhook consumer.

#### Task 11, step 6: Verify GREEN and commit

```bash
go test -count=1 ./internal/notify/... ./test/contract/...
go test -race -count=1 ./internal/notify/...
go test -count=1 ./...
git add internal/notify test/contract cmd/smoke-webhook
git commit -m "feat: deliver signed webhook notifications"
```

---

### Task 12: Build the controller reconciliation pipeline

**Objective:** Connect read-only watches to normalization, rules, optional
provider evaluation, persistence, and resolution while remaining idempotent.

**Files:**

- Create: `internal/kube/manager.go`
- Create: `internal/kube/informer_factory.go`
- Create: `internal/kube/event_handler.go`
- Create: `internal/kube/resolver.go`
- Create: `internal/controller/reconciler.go`
- Create: `internal/controller/reconciler_test.go`
- Create: `internal/controller/resolution_sweeper.go`
- Create: `internal/controller/resolution_sweeper_test.go`
- Create: `internal/app/app.go`
- Create: `internal/app/app_test.go`
- Modify: `cmd/ruleraven/main.go`

#### Task 12, step 1: Write a failing envtest pipeline test

Start envtest, create a failed Job, and assert one incident, one snapshot, one
evaluation, and one outbox entry through spy boundaries. Assert the informer
handler returns after queueing and makes no provider/store call inline.

#### Task 12, step 2: Verify RED

Run:

```bash
go test -count=1 -tags=integration ./internal/controller -run TestFailedJobPipeline
```

Expected: FAIL because the controller pipeline is absent.

#### Task 12, step 3: Implement incrementally

Add only explicit watches for Pods, Events, Deployments, StatefulSets,
DaemonSets, and Jobs. Namespace filters apply before queueing. Workers fetch
current state, normalize, fingerprint, and short-circuit equivalent snapshots.
Terminal decisions skip providers. Semantic results use primary then configured
fallback only. Provider failure creates the configured deterministic fallback
rather than dropping the incident.

Generate a unique evaluation key from incident, snapshot hash, policy version,
rubric version, and provider config hash. Handle optimistic-write conflicts by
requeueing from current state, not replaying stale output.

#### Task 12, step 4: Add idempotency and lifecycle tests

Assert:

- 20 equivalent updates create one evaluation/delivery;
- a material snapshot change updates the same incident;
- recovery and resource deletion create one resolution;
- Event-only incidents resolve after the injected quiet period;
- selected ambiguous Warning Events call the provider once;
- deterministic critical events never call the provider;
- shutdown stops queue acceptance, drains/cancels workers, and returns; and
- no path invokes Kubernetes update/delete for workloads.

#### Task 12, step 5: Verify GREEN

Run:

```bash
go test -count=1 -tags=integration ./internal/controller ./internal/kube
go test -race -count=1 ./internal/controller ./internal/app
go test -count=1 ./...
```

Expected: PASS with envtest lifecycle behavior and no live provider/Atlas use.

#### Task 12, step 6: Commit

```bash
git add internal/controller internal/kube internal/app cmd/ruleraven \
  go.mod go.sum
git commit -m "feat: reconcile Kubernetes incidents"
```

---

### Task 13: Add telemetry, readiness, and sensitive-data tests

**Objective:** Expose operational state without leaking snapshots or making
external dependency outages restart healthy workers.

**Files:**

- Create: `internal/telemetry/metrics.go`
- Create: `internal/telemetry/metrics_test.go`
- Create: `internal/telemetry/logging.go`
- Create: `internal/telemetry/logging_test.go`
- Modify: `internal/health/server.go`
- Modify: `internal/health/server_test.go`
- Modify: `internal/app/app.go`

#### Task 13, step 1: Write failing readiness transition tests

Assert `/readyz` returns 503 until config validation, Mongo ping/index
verification, and informer cache sync have all succeeded. Provider/webhook
outages must not change readiness after startup. `/healthz` reports only process
liveness.

#### Task 13, step 2: Verify RED

Run:

```bash
go test -count=1 ./internal/health -run TestReadiness
```

Expected: FAIL because readiness dependencies are absent.

#### Task 13, step 3: Implement probes and metrics

Expose Prometheus metrics for reconciles, duration, queue depth, incident
transitions, provider requests/retries, outbox pending, delivery attempts,
MongoDB operations, and latency. Keep metric labels bounded: provider type/model,
status, severity, rule, destination, and outcome; never incident/resource IDs.

#### Task 13, step 4: Add structured-log leak tests

Use sentinel credentials, request state, webhook body, raw provider response,
and unsafe Kubernetes fields. Exercise success and error paths and assert none
appears in captured logs. Logs may include cluster/incident/resource identity,
rule IDs, resolved model, request/delivery IDs, attempts, latency, and classified
errors.

#### Task 13, step 5: Verify GREEN and commit

```bash
go test -count=1 ./internal/health ./internal/telemetry ./internal/app
go test -count=1 ./...
git add internal/health internal/telemetry internal/app go.mod go.sum
git commit -m "feat: add controller telemetry and readiness"
```

---

### Task 14: Package a hardened container and least-privilege Helm chart

**Objective:** Render a secure two-replica deployment with explicit watch RBAC,
configuration, probes, and optional integrations.

**Files:**

- Create: `deploy/docker/Dockerfile`
- Create: `deploy/helm/ruleraven/Chart.yaml`
- Create: `deploy/helm/ruleraven/values.yaml`
- Create: `deploy/helm/ruleraven/values.schema.json`
- Create: `deploy/helm/ruleraven/templates/_helpers.tpl`
- Create: `deploy/helm/ruleraven/templates/deployment.yaml`
- Create: `deploy/helm/ruleraven/templates/serviceaccount.yaml`
- Create: `deploy/helm/ruleraven/templates/role.yaml`
- Create: `deploy/helm/ruleraven/templates/rolebinding.yaml`
- Create: `deploy/helm/ruleraven/templates/clusterrole.yaml`
- Create: `deploy/helm/ruleraven/templates/clusterrolebinding.yaml`
- Create: `deploy/helm/ruleraven/templates/service.yaml`
- Create: `deploy/helm/ruleraven/templates/configmap.yaml`
- Create: `deploy/helm/ruleraven/templates/servicemonitor.yaml`
- Create: `deploy/helm/ruleraven/templates/networkpolicy.yaml`
- Create: `deploy/helm/ruleraven/templates/poddisruptionbudget.yaml`
- Create: `deploy/helm/ruleraven/tests/render_test.yaml`
- Create: `deploy/helm/ruleraven/templates/tests/smoke-test.yaml`

#### Task 14, step 1: Write chart assertions before templates

Use a Helm unit-test plugin or a repository script to assert namespace mode has
only `get/list/watch` on Pods, Events, Deployments, StatefulSets, DaemonSets, and
Jobs; a separate Role allows Lease operations. Assert no Secrets, ConfigMaps,
pod logs, exec, Nodes, SubjectAccessReviews, TokenRequests, wildcard resources,
or workload write verbs.

#### Task 14, step 2: Verify RED

Run:

```bash
helm lint deploy/helm/ruleraven
helm unittest deploy/helm/ruleraven
```

Expected: FAIL because chart/templates do not yet exist.

#### Task 14, step 3: Implement chart and image

Use a pinned Go builder, `CGO_ENABLED=0`, `-trimpath`, version ldflags, and a
non-root distroless/static final image with no shell/source/credentials. Helm
must set read-only root filesystem, dropped capabilities, no privilege
escalation, RuntimeDefault seccomp, resource requests/limits, probes, graceful
termination, topology spread, and ConfigMap checksum rollout.

Default to namespace-scoped RBAC. Cluster-wide mode is opt-in with the same
explicit read verbs. Optional Event emission adds only Event create/patch.
Optional ServiceMonitor, NetworkPolicy, and PDB must not render when disabled.
Standard NetworkPolicy must not claim hostname-level egress filtering.

#### Task 14, step 4: Validate both variants

```bash
helm lint deploy/helm/ruleraven
helm unittest deploy/helm/ruleraven
helm template namespace deploy/helm/ruleraven \
  --set 'cluster.watchNamespaces[0]=default' > /tmp/ruleraven-namespace.yaml
helm template cluster deploy/helm/ruleraven \
  --set rbac.clusterWide=true > /tmp/ruleraven-cluster.yaml
kubeconform -strict /tmp/ruleraven-namespace.yaml /tmp/ruleraven-cluster.yaml
docker build -f deploy/docker/Dockerfile .
```

Expected: all checks pass; inspect the final image and confirm it runs non-root
and contains only the binary and required runtime files.

#### Task 14, step 5: Commit

```bash
git add deploy
git commit -m "build: package hardened controller deployment"
```

---

### Task 15: Prove behavior end to end in Kind

**Objective:** Exercise the real controller, Mongo transaction/outbox, signed
webhook, leader failover, and RBAC denials before calling the MVP complete.

**Files:**

- Create: `test/e2e/e2e_test.go`
- Create: `test/e2e/harness_test.go`
- Create: `test/e2e/fixtures/failing-job.yaml`
- Create: `test/e2e/fixtures/crashloop-pod.yaml`
- Create: `test/e2e/fixtures/ambiguous-event.yaml`
- Create: `test/e2e/fixtures/provider-stub.yaml`
- Create: `test/e2e/fixtures/mongo-replicaset.yaml`
- Modify: `Makefile`
- Modify: `deploy/helm/ruleraven/templates/tests/smoke-test.yaml`

#### Task 15, step 1: Write a failing end-to-end assertion

Create Kind, install two controller replicas, a test Mongo replica set, provider
stub, and smoke webhook. Apply the failed Job and wait with a bounded context for
one persisted open incident and one valid signed delivery.

#### Task 15, step 2: Verify RED

Run:

```bash
make e2e
```

Expected: FAIL before the harness/deployment path is complete, with cluster logs
and diagnostics retained.

#### Task 15, step 3: Complete the harness and acceptance matrix

Prove from real execution:

1. two controller pods become Ready and exactly one holds the Lease;
2. failed Job creates one incident, snapshot, evaluation, and delivery;
3. signature, timestamp, event ID, and envelope version validate;
4. equivalent Event replay creates no extra evaluation or delivery;
5. crash-loop threshold produces a deterministic decision without provider use;
6. ambiguous Warning Event calls the provider stub and stores typed answers;
7. malformed provider output produces configured `provider_unavailable` action;
8. retryable provider/webhook errors retry, permanent `4xx` errors do not;
9. restart after commit but before delivery loses no outbox work;
10. concurrent dispatchers do not hold the same active lease;
11. recovery/deletion creates exactly one resolved event;
12. deleting the leader causes takeover and continued processing;
13. `kubectl auth can-i` denies Secret reads, pod exec, and workload patch/delete;
14. all required unique/query/single-field TTL indexes exist; and
15. captured logs contain none of the seeded secret/snapshot sentinels.

#### Task 15, step 4: Verify GREEN and clean teardown

```bash
make e2e
go test -count=1 ./...
```

Expected: PASS. The harness must always export diagnostics on failure and delete
its Kind cluster unless `KEEP_E2E_CLUSTER=1` is set.

#### Task 15, step 5: Commit

```bash
git add test/e2e Makefile deploy/helm/ruleraven/templates/tests
git commit -m "test: verify RuleRaven end to end"
```

---

### Task 16: Add CI, supply-chain checks, and staged Atlas smoke testing

**Objective:** Make every documented verification reproducible and publish no
artifact until tests, provenance, and security gates pass.

**Files:**

- Create: `.github/workflows/ci.yaml`
- Create: `.github/workflows/e2e.yaml`
- Create: `.github/workflows/release.yaml`
- Create: `.github/dependabot.yml`
- Create: `scripts/atlas-smoke.sh`
- Modify: `README.md`
- Modify: `SECURITY.md`

#### Task 16, step 1: Add CI configuration tests first

Pin action versions to full commit SHAs and add a local workflow linter. Assert
pull requests run formatting, unit, race, vet, pinned static analysis,
`govulncheck`, integration, chart, manifest, image-build, and Kind checks.

#### Task 16, step 2: Verify RED

Run the chosen workflow linter and expected local CI target before workflow files
exist. Expected: FAIL because the required jobs are absent.

#### Task 16, step 3: Implement pull-request and release workflows

Cache only dependency/build artifacts, not secrets. Give jobs minimum GitHub
permissions. Release only immutable tags after repeating all gates. Build
`linux/amd64` and `linux/arm64`, generate an SBOM with Syft, scan with Trivy,
sign image/provenance with Cosign through GitHub OIDC, package/sign the Helm
chart, and attach checksums.

The Atlas smoke job must be manual or protected-environment only. It uses a
dedicated database name containing run ID, confirms other databases are
untouched, and deletes test collections/database during cleanup. It never prints
the URI.

#### Task 16, step 4: Verify workflows and docs

```bash
make ci
make e2e
# Run scripts/atlas-smoke.sh only in the protected staging environment.
```

Expected: local CI and e2e pass. A protected staging run connects to an actual
Atlas test cluster, uses only its dedicated database, and records sanitized
results.

#### Task 16, step 5: Commit

```bash
git add .github scripts README.md SECURITY.md
git commit -m "ci: verify and secure RuleRaven releases"
```

---

### Task 17: Perform MVP release-candidate audit

**Objective:** Verify every safety and behavior claim against evidence before
publishing the first Alpha release.

**Files:**

- Create: `docs/release/alpha-checklist.md`
- Create: `docs/operations/configuration.md`
- Create: `docs/operations/webhook-verification.md`
- Modify: `README.md`

#### Task 17, step 1: Write the checklist as failing evidence slots

For each acceptance item below, record the exact command, CI run URL or log
artifact, expected result, actual result, commit SHA, and reviewer. Empty evidence
means the item is not complete.

#### Task 17, step 2: Audit required evidence

Require proof for:

- all unit, race, integration, contract, envtest, Helm, manifest, image, and Kind
  tests;
- staging Atlas isolation and indexes;
- read-only RBAC denials and leader failover;
- no Secret watch, workload mutation, or hidden provider authority;
- normalized-state and log redaction sentinels;
- transactional outbox crash recovery and at-least-once wording;
- exact webhook signature verification and replay guidance;
- provider malformed-output and outage behavior;
- non-root image contents, SBOM, vulnerability scan, signatures, and provenance;
- all README quick-start commands executed from a clean checkout; and
- documentation still labels the release Alpha and avoids unsupported claims.

#### Task 17, step 3: Fix documentation from actual results

Replace planned quick-start language only for commands demonstrated on the
release commit. Keep unimplemented roadmap items clearly marked. Document known
limitations and upgrade/configuration compatibility honestly.

#### Task 17, step 4: Final verification and commit

```bash
make ci
make e2e
markdownlint-cli2 '**/*.md'
git diff --check
```

Expected: all commands pass, all evidence slots are filled, and two reviewers
confirm the safety boundaries before tagging.

```bash
git add docs README.md
git commit -m "docs: document Alpha operations and release evidence"
```

---

## MVP acceptance criteria

The MVP is complete only after Tasks 1–17 have executable evidence for all of
the following:

1. A Helm install creates two Ready replicas and exactly one leader.
2. The ServiceAccount reads only the explicit resource set; Secret reads, pod
   exec, and workload mutation are denied.
3. A failed Job creates one open incident, immutable snapshot, evaluation, and
   outbox entry per destination.
4. An exact webhook body validates against its HMAC signature, timestamp, event
   ID, and versioned envelope.
5. Equivalent observations do not create duplicate decisions or deliveries.
6. A threshold-crossing crash loop produces deterministic severity without a
   provider call.
7. An ambiguous Warning Event uses the configured provider and stores only
   validated typed answers and audit metadata.
8. Invalid provider output becomes the configured provider-unavailable fallback
   and is never heuristically parsed.
9. Retryable failures retry with bounded backoff; permanent failures do not.
10. A crash between decision commit and delivery does not lose the notification.
11. Two workers cannot hold the same unexpired outbox lease.
12. Recovery, deletion, and Event quiet-period resolution emit exactly one
    resolution transition.
13. Leader loss causes takeover without violating idempotency.
14. MongoDB has the exact unique/query/single-field TTL indexes and uses only the
    explicitly named deployment database.
15. Logs and stored documents contain no seeded credentials, raw provider state,
    complete Kubernetes objects, or excluded sensitive fields.
16. Readiness waits for config, Mongo/index verification, and informer cache sync
    but remains true during downstream provider/notifier outages.
17. All unit, race, integration, contract, envtest, Helm, manifest, image, and
    Kind checks pass in CI.
18. A protected smoke test exercises a dedicated Atlas test database without
    touching any other database.
19. A tagged Alpha release publishes signed multi-architecture images, a signed
    chart, SBOM, checksums, and provenance.
20. The README quick start has been executed from a clean checkout and describes
    only behavior present in that exact release.

## Explicitly out of scope for the MVP

- automatic remediation or workload mutation;
- a dashboard, public incident-management API, or acknowledgement workflow;
- runtime-loaded Go `.so` plugins;
- watching Secrets or persisting arbitrary full Kubernetes objects;
- pod-log collection, pod execution, node inspection, or cross-cluster
  correlation;
- training or fine-tuning models;
- multi-provider voting; and
- any exactly-once delivery guarantee.
