# Changelog

All notable changes to RuleRaven are documented here while the project is in
Alpha. The format follows [Keep a Changelog](https://keepachangelog.com/), but
there is no stable compatibility promise yet.

## [Unreleased]

### Added

- Working Go controller for allowlisted Kubernetes workload and Event watches.
- Strict normalization, redaction, deterministic rules, and provider-neutral
  typed decision handling.
- TypeSafe, OpenRouter, OpenAI, Anthropic, and generic OpenAI-compatible provider
  adapters with shared contract tests.
- MongoDB transactional incident, evaluation, snapshot, and outbox persistence.
- HMAC-signed generic webhook delivery, readiness, health, and Prometheus-format
  telemetry.
- Hardened container build, least-privilege Helm chart, integration tests, and
  GitHub CI.

### Known limitations

- No automatic remediation or leader election; deployments must use one replica.
- Alpha interfaces, configuration, and artifacts are not production-supported.

[Unreleased]: https://github.com/ddalcero/ruleraven/commits/main
