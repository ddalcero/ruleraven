# RuleRaven use cases and recipes

These documents show practical ways to compose RuleRaven with decision providers
and external notification consumers. They use synthetic examples and omit private
cluster identifiers, credentials, endpoints, customer data, and personal details.

## Published

- [Jev classification with Hermes incident investigation](hermes-jev-incident-triage.md)
  — deterministic rules first, pinned Jev classification for ambiguous signals,
  signed webhook delivery, and concise live verification through a narrow
  read-only Kubernetes connector.

## Candidate recipes

The generic signed webhook also supports smaller integrations that can be
published independently:

- n8n verification, deduplication, severity routing, and chat or ticket delivery;
- incident-management alert creation and recovery closure;
- archive-only audit ingestion; and
- custom read-only enrichment services.

A recipe should preserve RuleRaven's boundaries: provider-neutral notifications,
verified signatures, idempotent consumers, bounded payloads, external credentials,
and no autonomous cluster mutation.
