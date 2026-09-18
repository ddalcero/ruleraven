# Safe Alpha test deployment

This runbook deploys RuleRaven for an isolated smoke test. It is not a
production guide. Use a disposable namespace, an immutable Alpha image tag,
synthetic incidents, and credentials dedicated to the test.

## Safety requirements

Before deploying:

- use a cluster and MongoDB database that you are authorized to test;
- keep the enforced `replicaCount: 1`; the chart uses `Recreate` upgrades because
  leader election is not implemented;
- start in namespace-only mode and grant cluster-wide reads only when the test
  requires them;
- never commit Secret manifests, rendered Secrets, provider keys, MongoDB URIs,
  webhook secrets, real webhook URLs, or copied customer resources;
- use HTTPS for external webhooks and a randomly generated shared secret;
- pin an immutable image tag and review the image source; and
- arrange a cleanup window and owner before creating resources.

RuleRaven is observation and notification software. It does not remediate,
restart, patch, delete, execute in, or scale workloads.

## Prerequisites

You need:

- Kubernetes 1.25 or later, `kubectl`, and Helm 3;
- a RuleRaven image built from a reviewed commit;
- a MongoDB replica set reachable from the cluster;
- one supported provider credential; and
- optionally, an HTTPS endpoint that accepts the generic signed webhook.

MongoDB transactions do not work against a standalone `mongod`. A single-node
replica set is adequate for a disposable smoke test, but it is not a durable or
high-availability deployment.

## Atlas permissions and database isolation

RuleRaven requires an explicit, non-administrative database name. It rejects an
empty name and `admin`, `local`, or `config`. Use a new name dedicated to the
cluster or test deployment.

Atlas provisioning is separate from the RuleRaven runtime:

1. The Atlas user or API key performing setup must have project permission to
   create or manage the required test deployment, network access, and database
   user. A database username/password does not grant Atlas control-plane
   permission.
2. The MongoDB database user supplied to RuleRaven must be authorized to read,
   write, create collections, and create indexes in the explicitly named test
   database. A scoped `readWrite` role on that database is the normal minimum.
3. The deployment must support replica-set transactions, and cluster network
   policy and the Atlas access list must permit the connection.
4. RuleRaven does not need an Atlas API key at runtime. It needs only the scoped
   MongoDB connection URI.

The Alpha EKS smoke test could not provision its intended Atlas test database
because the available Atlas API keys and database users lacked the required
control-plane and database permissions. It therefore used an ephemeral
single-node in-cluster MongoDB replica set. Do not silently fall back this way
for durable environments; record the exception and data-loss implications.

## Provider choices

Set `decision.primary.type` to one production-registered provider:

| Type | Protocol | Credential |
| --- | --- | --- |
| `typesafe` | Direct TypeSafe System One/Jev | TypeSafe API key |
| `openrouter` | OpenRouter Decisions/Jev | OpenRouter API key |
| `openai` | Strict JSON Schema chat output | OpenAI API key |
| `anthropic` | Forced answer tool | Anthropic API key |

The generic `openai-compatible` adapter is registered in production. Set its
absolute HTTPS `endpoint` and select `strictMode=json_schema` or
`strictMode=forced_tool`. Native providers reject endpoint overrides, and
unstructured output modes are rejected.

A fallback is optional. If enabled, it must use a different provider type and a
separate Secret key reference. RuleRaven sends only normalized, redacted
snapshots, but operators must still review provider data-handling terms and
regional requirements.

## Create the namespace and Secret

Choose non-sensitive identifiers. Export credentials into the current shell
without placing them in a values file:

```bash
export NAMESPACE='ruleraven-test'
export MONGODB_URI='<scoped-replica-set-uri>'
export PROVIDER_API_KEY='<test-provider-api-key>'
export WEBHOOK_SECRET='<random-shared-secret>'

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml |
  kubectl apply -f -

kubectl --namespace "$NAMESPACE" create secret generic ruleraven-credentials \
  --from-literal=mongodb-uri="$MONGODB_URI" \
  --from-literal=provider-api-key="$PROVIDER_API_KEY" \
  --from-literal=webhook-secret="$WEBHOOK_SECRET" \
  --dry-run=client -o yaml |
  kubectl apply -f -
```

Unset the shell variables after deployment. Avoid shell tracing while handling
them. The example Secret keys are:

| Key | Required when | Mounted environment variable |
| --- | --- | --- |
| `mongodb-uri` | Always | `MONGODB_URI` |
| `provider-api-key` | Always | `PROVIDER_KEY` |
| `fallback-api-key` | Fallback enabled | `FALLBACK_PROVIDER_KEY` |
| `webhook-secret` | Webhook enabled | `WEBHOOK_SECRET` |

The key names are configurable under each `existingSecret.key`; the environment
variable names inside the controller are fixed by the chart. Use an external
Secret controller if that is the cluster standard, but verify that the final
Secret and keys exist before installing RuleRaven.

## Prepare non-secret values

Create a local file outside the repository, for example
`/tmp/ruleraven-test-values.yaml`:

```yaml
replicaCount: 1

image:
  repository: ghcr.io/ddalcero/ruleraven
  tag: "<immutable-alpha-tag>"

rbac:
  clusterWide: false

cluster:
  id: isolated-test-cluster
  watchNamespaces:
    - ruleraven-test

mongo:
  database: ruleraven_isolated_test
  existingSecret:
    name: ruleraven-credentials
    key: mongodb-uri

decision:
  primary:
    type: openrouter
    model: typesafe/jev-1.13
    existingSecret:
      name: ruleraven-credentials
      key: provider-api-key
  fallback:
    enabled: false

notifications:
  webhook:
    enabled: false
    url: https://example.invalid/ruleraven
    existingSecret:
      name: ruleraven-credentials
      key: webhook-secret
```

Do not put a credential, signed URL, connection URI, or live webhook hostname in
this file if it may be committed, uploaded, or attached to an issue.

## Review RBAC and external authorization

Render and inspect before applying:

```bash
helm lint --strict deploy/helm/ruleraven
helm template ruleraven deploy/helm/ruleraven \
  --namespace "$NAMESPACE" \
  --values /tmp/ruleraven-test-values.yaml > /tmp/ruleraven-rendered.yaml
python3 deploy/helm/ruleraven/tests/chart_assertions.py
```

The chart assertions verify the intended least-privilege Role or ClusterRole,
but rendered RBAC is not necessarily the service account's effective policy.
Admission systems or cluster-wide external webhook authorizers can grant broader
access. After installation, check the effective permissions:

```bash
kubectl auth can-i --list \
  --as="system:serviceaccount:${NAMESPACE}:ruleraven" \
  --namespace="$NAMESPACE"

kubectl auth can-i get secrets \
  --as="system:serviceaccount:${NAMESPACE}:ruleraven" \
  --namespace="$NAMESPACE"

kubectl auth can-i create pods/exec \
  --as="system:serviceaccount:${NAMESPACE}:ruleraven" \
  --namespace="$NAMESPACE"
```

The last two commands must return `no`. If another authorizer grants broader
permissions than the chart roles, stop the test and correct or explicitly
isolate that cluster-level policy. Do not describe the deployment as
least-privilege based only on the Helm templates.

## Install and verify

```bash
helm upgrade --install ruleraven deploy/helm/ruleraven \
  --namespace "$NAMESPACE" \
  --values /tmp/ruleraven-test-values.yaml \
  --wait --timeout 5m

kubectl --namespace "$NAMESPACE" rollout status deployment/ruleraven \
  --timeout=5m
kubectl --namespace "$NAMESPACE" get pods,service
kubectl --namespace "$NAMESPACE" logs deployment/ruleraven --tail=100
```

Forward the service only to localhost and inspect probes and metrics:

```bash
kubectl --namespace "$NAMESPACE" port-forward service/ruleraven 18080:8080
```

In another shell:

```bash
curl --fail --silent --show-error http://127.0.0.1:18080/healthz
curl --fail --silent --show-error http://127.0.0.1:18080/readyz
curl --fail --silent --show-error http://127.0.0.1:18080/metrics
```

Readiness confirms configuration validation, MongoDB readiness and index
verification, and informer synchronization. It does not prove that an external
provider or webhook is currently available.

Use only synthetic test objects to exercise a deterministic rule and one
ambiguous provider decision. Verify the named MongoDB database contains the
expected indexes and outbox records without printing document bodies or the
connection URI into logs.

## Optional Bastion or generic webhook

Bastion is an optional consumer of RuleRaven's generic webhook. It is not a
control plane, provider, or RuleRaven-specific plugin. Any compatible consumer
can be used.

To enable it, set `notifications.webhook.enabled: true`, configure its HTTPS URL
outside committed files, and make its `existingSecret.key` point to the shared
HMAC secret. RuleRaven sends these signature headers over the exact request
body:

- `X-Webhook-Timestamp` and `X-Webhook-Signature-V2` for generic HMAC V2
  consumers, including compatible Bastion receivers; and
- `X-RuleRaven-Timestamp` and `X-RuleRaven-Signature` for the namespaced form.

The signature input is `<unix-seconds>.<raw-body>` and uses HMAC-SHA256. A
consumer must verify the raw bytes before parsing, enforce a short timestamp
window, and deduplicate with `X-RuleRaven-Event-ID` or the envelope event ID.
Delivery is at-least-once.

A quick Cloudflare tunnel may be used to expose a local receiver during a short,
supervised smoke test. Its URL is temporary, should never be committed, and is
not suitable for unattended or production delivery. Prefer a stable private or
managed HTTPS ingress with access controls and monitoring.

## Cleanup

Export any non-sensitive test evidence first, then remove the release and test
resources:

```bash
helm uninstall ruleraven --namespace "$NAMESPACE"
kubectl delete namespace "$NAMESPACE"
rm -f /tmp/ruleraven-test-values.yaml /tmp/ruleraven-rendered.yaml
unset MONGODB_URI PROVIDER_API_KEY WEBHOOK_SECRET NAMESPACE
```

Delete the dedicated Atlas database user, network access entry, and test
database or deployment according to the approved retention plan. Rotate the
provider and webhook credentials if they were exposed to command history,
terminal recording, logs, or an issue attachment.
