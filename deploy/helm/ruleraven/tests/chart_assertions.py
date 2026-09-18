#!/usr/bin/env python3
"""Render the RuleRaven chart and assert its security/RBAC contract."""

from __future__ import annotations

import pathlib
import subprocess
import sys
from typing import Any

import yaml

CHART = pathlib.Path(__file__).resolve().parents[1]
READ_RESOURCES = {
    ("", "pods"),
    ("", "events"),
    ("apps", "deployments"),
    ("apps", "statefulsets"),
    ("apps", "daemonsets"),
    ("batch", "jobs"),
}
FORBIDDEN_RESOURCES = {
    "secrets",
    "configmaps",
    "pods/log",
    "pods/exec",
    "nodes",
    "subjectaccessreviews",
    "tokenreviews",
    "serviceaccounts/token",
    "tokenrequests",
    "*",
}
FORBIDDEN_VERBS = {"create", "patch", "delete", "deletecollection", "update", "bind", "escalate", "impersonate", "*"}


def render(release: str, *settings: str) -> list[dict[str, Any]]:
    command = ["helm", "template", release, str(CHART), "--namespace", "ruleraven-test"]
    for setting in settings:
        command.extend(["--set", setting])
    output = subprocess.run(command, check=True, text=True, capture_output=True).stdout
    return [document for document in yaml.safe_load_all(output) if isinstance(document, dict)]


def render_fails(release: str, *settings: str) -> None:
    command = ["helm", "template", release, str(CHART), "--namespace", "ruleraven-test"]
    for setting in settings:
        command.extend(["--set", setting])
    result = subprocess.run(command, check=False, text=True, capture_output=True)
    assert result.returncode != 0, f"expected Helm schema rejection for {settings}"


def one(documents: list[dict[str, Any]], kind: str, suffix: str | None = None) -> dict[str, Any]:
    matches = [document for document in documents if document.get("kind") == kind]
    if suffix is not None:
        matches = [document for document in matches if document["metadata"]["name"].endswith(suffix)]
    assert len(matches) == 1, f"expected one {kind} {suffix or ''}, got {len(matches)}"
    return matches[0]


def kinds(documents: list[dict[str, Any]]) -> set[str]:
    return {str(document.get("kind")) for document in documents}


def assert_rules(rules: list[dict[str, Any]]) -> None:
    observed: set[tuple[str, str]] = set()
    for rule in rules:
        verbs = set(rule.get("verbs", []))
        resources = set(rule.get("resources", []))
        assert not (resources & FORBIDDEN_RESOURCES), (resources, "forbidden resources")
        assert verbs <= {"get", "list", "watch"}, (verbs, "unexpected verbs")
        assert not (verbs & FORBIDDEN_VERBS), (verbs, "forbidden verbs")
        for group in rule.get("apiGroups", []):
            for resource in resources:
                observed.add((group, resource))
    assert READ_RESOURCES <= observed, f"missing read resources: {READ_RESOURCES - observed}"


def assert_no_unsupported_rbac(documents: list[dict[str, Any]]) -> None:
    for document in documents:
        if document.get("kind") not in {"Role", "ClusterRole"}:
            continue
        for rule in document.get("rules", []):
            assert "leases" not in rule.get("resources", []), "leader-election Lease access is unsupported"
            assert not (set(rule.get("verbs", [])) & FORBIDDEN_VERBS), "write RBAC is unsupported"


def assert_deployment(documents: list[dict[str, Any]]) -> None:
    deployment = one(documents, "Deployment")
    assert deployment["spec"]["replicas"] == 1
    assert deployment["spec"]["strategy"] == {"type": "Recreate"}, "leaderless controller upgrades must not overlap"
    pod = deployment["spec"]["template"]
    assert "checksum/config" in pod["metadata"]["annotations"]
    spec = pod["spec"]
    assert spec["automountServiceAccountToken"] is True
    assert spec["securityContext"]["runAsNonRoot"] is True
    assert spec["securityContext"]["seccompProfile"]["type"] == "RuntimeDefault"
    assert spec["imagePullSecrets"] == [{"name": "registry-auth"}]
    container = spec["containers"][0]
    security = container["securityContext"]
    assert security["allowPrivilegeEscalation"] is False
    assert security["readOnlyRootFilesystem"] is True
    assert security["runAsNonRoot"] is True
    assert security["capabilities"]["drop"] == ["ALL"]
    assert container["livenessProbe"]["httpGet"]["path"] == "/healthz"
    assert container["readinessProbe"]["httpGet"]["path"] == "/readyz"
    assert container["resources"]["requests"] and container["resources"]["limits"]
    assert spec["topologySpreadConstraints"]
    env_names = {item["name"] for item in container["env"]}
    assert {"MONGODB_URI", "PROVIDER_KEY"} <= env_names


def assert_namespace_mode() -> None:
    documents = render(
        "namespace",
        "cluster.watchNamespaces[0]=default",
        "imagePullSecrets[0].name=registry-auth",
    )
    assert "ClusterRole" not in kinds(documents)
    assert "ClusterRoleBinding" not in kinds(documents)
    watcher = one(documents, "Role", "-watcher")
    assert_rules(watcher["rules"])
    assert_no_unsupported_rbac(documents)
    assert_deployment(documents)
    assert {"Service", "ServiceAccount", "ConfigMap", "Pod"} <= kinds(documents)
    assert not ({"ServiceMonitor", "NetworkPolicy", "PodDisruptionBudget"} & kinds(documents))
    config = yaml.safe_load(one(documents, "ConfigMap")["data"]["config.yaml"])
    assert config["cluster"]["clusterWide"] is False
    assert config["cluster"]["namespaceOnly"] is True
    assert config["cluster"]["watchNamespaces"] == ["default"]


def assert_cluster_mode() -> None:
    documents = render(
        "cluster",
        "rbac.clusterWide=true",
        "cluster.watchNamespaces={}",
        "imagePullSecrets[0].name=registry-auth",
    )
    assert not [d for d in documents if d.get("kind") == "Role" and d["metadata"]["name"].endswith("-watcher")]
    watcher = one(documents, "ClusterRole", "-watcher")
    assert_rules(watcher["rules"])
    binding = one(documents, "ClusterRoleBinding", "-watcher")
    assert binding["roleRef"]["name"] == watcher["metadata"]["name"]
    assert_no_unsupported_rbac(documents)
    config = yaml.safe_load(one(documents, "ConfigMap")["data"]["config.yaml"])
    assert config["cluster"]["clusterWide"] is True
    assert config["cluster"]["namespaceOnly"] is False
    assert config["cluster"]["watchNamespaces"] == []


def assert_optional_resources() -> None:
    documents = render(
        "optional",
        "cluster.watchNamespaces[0]=default",
        "imagePullSecrets[0].name=registry-auth",
        "serviceMonitor.enabled=true",
        "networkPolicy.enabled=true",
        "podDisruptionBudget.enabled=true",
    )
    assert {"ServiceMonitor", "NetworkPolicy", "PodDisruptionBudget"} <= kinds(documents)
    watcher = one(documents, "Role", "-watcher")
    assert_rules(watcher["rules"])
    assert_no_unsupported_rbac(documents)
    policy = one(documents, "NetworkPolicy")
    assert policy["spec"]["policyTypes"] == ["Ingress", "Egress"]
    egress = policy["spec"]["egress"]
    assert egress, "enabled policy must make egress intent explicit"


def assert_openai_compatible_configuration() -> None:
    documents = render(
        "compatible",
        "decision.primary.type=openai-compatible",
        "decision.primary.model=private-model",
        "decision.primary.endpoint=https://gateway.example.com",
        "decision.primary.strictMode=forced_tool",
    )
    config = yaml.safe_load(one(documents, "ConfigMap")["data"]["config.yaml"])
    primary = config["decision"]["primary"]
    assert primary["endpoint"] == "https://gateway.example.com"
    assert primary["strictMode"] == "forced_tool"
    fallback_documents = render(
        "compatible-fallback",
        "decision.fallback.enabled=true",
        "decision.fallback.type=openai-compatible",
        "decision.fallback.model=private-model",
        "decision.fallback.endpoint=https://fallback.example.com",
        "decision.fallback.strictMode=json_schema",
    )
    fallback = yaml.safe_load(one(fallback_documents, "ConfigMap")["data"]["config.yaml"])["decision"]["fallback"]
    assert fallback["endpoint"] == "https://fallback.example.com"
    assert fallback["strictMode"] == "json_schema"
    render_fails(
        "compatible-missing-endpoint",
        "decision.primary.type=openai-compatible",
        "decision.primary.model=private-model",
        "decision.primary.strictMode=json_schema",
    )
    render_fails(
        "compatible-insecure-endpoint",
        "decision.primary.type=openai-compatible",
        "decision.primary.model=private-model",
        "decision.primary.endpoint=http://gateway.example.com",
        "decision.primary.strictMode=json_schema",
    )
    render_fails(
        "compatible-fallback-missing-endpoint",
        "decision.fallback.enabled=true",
        "decision.fallback.type=openai-compatible",
        "decision.fallback.model=private-model",
        "decision.fallback.strictMode=json_schema",
    )
    for release, setting in (
        ("compatible-invalid-mode", "decision.primary.strictMode=text"),
        ("compatible-userinfo", "decision.primary.endpoint=https://user@gateway.example.com"),
        ("compatible-query", "decision.primary.endpoint=https://gateway.example.com?token=x"),
        ("compatible-fragment", "decision.primary.endpoint=https://gateway.example.com#fragment"),
    ):
        render_fails(
            release,
            "decision.primary.type=openai-compatible",
            "decision.primary.model=private-model",
            "decision.primary.endpoint=https://gateway.example.com" if "endpoint=" not in setting else setting,
            "decision.primary.strictMode=json_schema" if "strictMode=" not in setting else setting,
        )
    render_fails("native-endpoint-override", "decision.primary.endpoint=https://evil.example.com")
    render_fails("native-mode-override", "decision.primary.strictMode=json_schema")
    render_fails("unsupported-replicas", "replicaCount=2")


def main() -> int:
    assert_namespace_mode()
    assert_cluster_mode()
    assert_optional_resources()
    assert_openai_compatible_configuration()
    print("chart assertions: PASS (namespace, cluster, providers, optional resources, hardened pod, RBAC)")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (AssertionError, subprocess.CalledProcessError) as error:
        print(f"chart assertions: FAIL: {error}", file=sys.stderr)
        raise
