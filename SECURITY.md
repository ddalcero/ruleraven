# Security Policy

## Project maturity

RuleRaven is **Alpha / under active development**. The repository contains a
working controller, container build, and Helm chart, but no version is currently
production-supported. Interfaces and data formats may change without notice,
and Alpha artifacts must not be treated as production-ready.

## Supported versions

No version is supported yet. Once releases begin, this table will identify the
versions receiving security updates:

| Version          | Supported             |
| ---------------- | --------------------- |
| Unreleased Alpha | No production support |

The project expects to provide security fixes for the latest released minor
version after a stable release policy is adopted. Old development snapshots and
unreleased builds will not receive backports unless maintainers state otherwise.

## Reporting a vulnerability

Do **not** open a public issue, discussion, or pull request for a suspected
vulnerability.

Use GitHub's private vulnerability reporting for this repository:

1. Open the repository's **Security** tab.
2. Select **Report a vulnerability**.
3. Include the information requested below.

If private vulnerability reporting is not available, contact the repository
owner privately through a contact method listed on their GitHub profile. Share
only enough information in the first message to establish a secure reporting
channel; do not post exploit details publicly.

A useful report includes:

- the affected commit, tag, component, and configuration;
- prerequisites and a minimal reproduction;
- the expected and observed security boundary;
- realistic impact, including data or permissions exposed;
- logs or payloads with credentials and personal data removed;
- suggested mitigations, if known; and
- whether you plan to request a CVE or coordinate disclosure elsewhere.

Please do not test against clusters, Atlas databases, providers, webhooks, or
accounts you do not own or have explicit permission to assess. Do not exfiltrate
or retain data beyond what is necessary to demonstrate impact.

## What to expect

Maintainers will aim to acknowledge a complete report within five business days.
Because this is a volunteer, pre-release project, that target is not a service
level agreement. The maintainers will then:

1. confirm a private communication channel;
2. reproduce and assess the issue;
3. agree on disclosure timing with the reporter where practical;
4. prepare tests, a fix, and affected-version guidance; and
5. publish an advisory and credit the reporter unless anonymity is requested.

Please allow time for a coordinated fix before public disclosure. If a report is
not considered a vulnerability, maintainers will explain why and may suggest a
normal issue after sensitive details are removed.

## Security boundaries

The intended design has the following hard boundaries. A violation is likely
security-relevant:

- RuleRaven does not automatically remediate or mutate workloads.
- Workload access is read-only and allowlisted; Secret reads, pod execution,
  pod logs, nodes, and wildcard RBAC are excluded.
- Kubernetes writes are limited to leader-election Leases and explicitly enabled
  Event emission.
- Snapshots are allowlisted and redacted rather than complete object dumps.
- Provider state, raw responses, API keys, webhook secrets, and Atlas connection
  strings are not logged or persisted by default.
- Provider output is untrusted, schema-validated evidence and cannot downgrade a
  deterministic critical result.
- Webhook targets are startup configuration, require HTTPS by default, reject
  redirects by default, and use bounded I/O.
- Notification delivery is at-least-once; signatures authenticate payloads but
  consumers must also enforce timestamp windows and event-ID deduplication.
- Incident transitions, evaluations, and notification outbox work are committed
  atomically.

Likely report categories include RBAC escalation, Secret or credential exposure,
normalization/redaction bypass, SSRF, signature confusion, provider-output
validation bypass, cross-deployment MongoDB access, outbox duplication that
breaks documented guarantees, dependency compromise, and release artifact
provenance failures.

General feature requests, reliability bugs without a security impact, and
questions about the planned architecture belong in public issues once sensitive
details have been removed.

## Dependency and release posture

CI runs formatting, unit and contract tests, race tests, vet, Docker-backed
MongoDB integration tests, Helm assertions, and an image build. Dependabot
tracks Go modules, GitHub Actions, and Docker bases. Vulnerability scanning,
SBOM generation, image signing, and provenance are still roadmap items. Until
signed releases are published, do not assume that an image, chart, binary, or
package claiming to be RuleRaven is an official project artifact.
