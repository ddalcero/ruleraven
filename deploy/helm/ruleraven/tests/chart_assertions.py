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
FORBIDDEN_VERBS = {"delete", "deletecollection", "update", "bind", "escalate", "impersonate", "*"}


def render(release: str, *settings: str) -> list[dict[str, Any]]:
    command = ["helm", "template", release, str(CHART), "--namespace", "ruleraven-test"]
    for setting in settings:
        command.extend(["--set", setting])
    output = subprocess.run(command, check=True, text=True, capture_output=True).stdout
    return [document for document in yaml.safe_load_all(output) if isinstance(document, dict)]


def one(documents: list[dict[str, Any]], kind: str, suffix: str | None = None) -> dict[str, Any]:
    matches = [document for document in documents if document.get("kind") == kind]
    if suffix is not None:
        matches = [document for document in matches if document["metadata"]["name"].endswith(suffix)]
    assert len(matches) == 1, f"expected one {kind} {suffix or ''}, got {len(matches)}"
    return matches[0]


def kinds(documents: list[dict[str, Any]]) -> set[str]:
    return {str(document.get("kind")) for document in documents}


def assert_rules(rules: list[dict[str, Any]], *, events_write: bool) -> None:
    observed: set[tuple[str, str]] = set()
    for rule in rules:
        verbs = set(rule.get("verbs", []))
        resources = set(rule.get("resources", []))
        assert not (resources & FORBIDDEN_RESOURCES), (resources, "forbidden resources")
        allowed_verbs = {"get", "list", "watch"}
        if events_write and resources == {"events"}:
            allowed_verbs |= {"create", "patch"}
        assert verbs <= allowed_verbs, (verbs, "unexpected verbs")
        assert not (verbs & FORBIDDEN_VERBS), (verbs, "forbidden verbs")
        for group in rule.get("apiGroups", []):
            for resource in resources:
                observed.add((group, resource))
    assert READ_RESOURCES <= observed, f"missing read resources: {READ_RESOURCES - observed}"


def assert_lease_role(documents: list[dict[str, Any]]) -> None:
    role = one(documents, "Role", "-leader-election")
    assert role["rules"] == [{
        "apiGroups": ["coordination.k8s.io"],
        "resources": ["leases"],
        "verbs": ["get", "list", "watch", "create", "update", "patch"],
    }]
    binding = one(documents, "RoleBinding", "-leader-election")
    assert binding["roleRef"]["name"] == role["metadata"]["name"]


def assert_deployment(documents: list[dict[str, Any]]) -> None:
    deployment = one(documents, "Deployment")
    assert deployment["spec"]["replicas"] == 1
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
    assert_rules(watcher["rules"], events_write=False)
    assert_lease_role(documents)
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
    assert_rules(watcher["rules"], events_write=False)
    binding = one(documents, "ClusterRoleBinding", "-watcher")
    assert binding["roleRef"]["name"] == watcher["metadata"]["name"]
    assert_lease_role(documents)
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
        "rbac.emitEvents=true",
    )
    assert {"ServiceMonitor", "NetworkPolicy", "PodDisruptionBudget"} <= kinds(documents)
    watcher = one(documents, "Role", "-watcher")
    assert_rules(watcher["rules"], events_write=True)
    policy = one(documents, "NetworkPolicy")
    assert policy["spec"]["policyTypes"] == ["Ingress", "Egress"]
    egress = policy["spec"]["egress"]
    assert egress, "enabled policy must make egress intent explicit"


def main() -> int:
    assert_namespace_mode()
    assert_cluster_mode()
    assert_optional_resources()
    print("chart assertions: PASS (namespace, cluster, optional resources, hardened pod, RBAC)")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (AssertionError, subprocess.CalledProcessError) as error:
        print(f"chart assertions: FAIL: {error}", file=sys.stderr)
        raise
