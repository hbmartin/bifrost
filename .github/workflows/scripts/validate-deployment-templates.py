#!/usr/bin/env python3
"""Validate Bifrost's Render Blueprints and Railway template contracts."""

from __future__ import annotations

import json
import re
import sys
from datetime import date
from pathlib import Path
from typing import Any, NoReturn
from urllib.parse import parse_qsl, urlsplit

import yaml
from jsonschema import Draft201909Validator, Draft202012Validator, FormatChecker


REPO_ROOT = Path(__file__).resolve().parents[3]
BIFROST_SCHEMA = json.loads((REPO_ROOT / "transports/config.schema.json").read_text())
RAILWAY_SCHEMA = json.loads((REPO_ROOT / "deploy/railway/template-contract.schema.json").read_text())
RENDER_VERIFICATION_SCHEMA = json.loads(
    (REPO_ROOT / "deploy/render/blueprint-verification.schema.json").read_text()
)
# Shared with the release gate in verify-deployment-release.sh, which refuses to
# qualify anything older. Read from one file so a bump cannot land in the gate
# and leave the recorded verifications accepting the release it just retired.
MINIMUM_RELEASE_TAG = json.loads((REPO_ROOT / "deploy/runtime-contract.json").read_text())["minimum_release_tag"]
RELEASE_TAG_PATTERN = re.compile(r"^v(\d+)\.(\d+)\.(\d+)$")
HOSTED_ONE_CLICK_HEADING = "## Hosted one-click choices"
HTTP_URL_PATTERN = re.compile(r"https?://[^\s)\"'<>\]`]+", re.IGNORECASE)
# Fenced blocks and inline code spans describe URLs rather than publish them: a
# reader cannot click prose set in code formatting, so the sweep must not read
# a documented URL shape as a live deploy button.
CODE_SPAN_PATTERN = re.compile(r"```.*?```|`[^`\n]*`", re.DOTALL)
# Sentence punctuation the URL regex cannot distinguish from the URL itself.
TRAILING_PUNCTUATION = ".,;:!?"
RENDER_BRANCH_PATTERN = re.compile(r"^[A-Za-z0-9._/-]+$")
# The single registry of publishable blueprints/contracts: main() validates the
# content of every entry, and one_click_targets() requires the recorded
# verification evidence to cover exactly these slots — adding a slot here
# cannot green-light a button without its content checks running.
RENDER_BLUEPRINT_PATHS = {
    "postgres": "render.yaml",
    "sqlite": "deploy/render/render-sqlite.yaml",
}
RAILWAY_TEMPLATE_CONTRACTS = {
    "postgres": ("PostgreSQL", "deploy/railway/postgres.template-contract.json"),
    "sqlite": ("SQLite", "deploy/railway/sqlite.template-contract.json"),
}
DeployButtonKey = tuple[str, str]


def fail(message: str) -> NoReturn:
    raise AssertionError(message)


def release_tuple(tag: str) -> tuple[int, ...] | None:
    """The ordering key for a release tag, or None if it is not one."""
    match = RELEASE_TAG_PATTERN.match(tag)
    return tuple(int(part) for part in match.groups()) if match else None


def validate_render_branch(branch: str, label: str) -> None:
    """Require a Git branch that can be embedded in a deploy URL verbatim."""
    components = branch.split("/")
    if (
        not RENDER_BRANCH_PATTERN.fullmatch(branch)
        or ".." in branch
        or any(
            not component
            or component.startswith(".")
            or component.endswith(".")
            or component.endswith(".lock")
            for component in components
        )
    ):
        fail(
            f"{label} branch {branch!r} cannot be embedded safely as a Git ref; "
            "use only letters, digits, '.', '_', '/', and '-', without empty, dot, or .lock components"
        )


def parse_deploy_button_url(url: str, label: str) -> DeployButtonKey | None:
    """Return the semantic identity of a Render/Railway deploy button.

    Hostnames and schemes are compared case-insensitively, while fragments,
    credentials, ports, duplicate query parameters, and ambiguous nested URLs
    are rejected. This makes the verification record bind to what the browser
    actually sends to the deployment platform rather than to a string suffix.
    """
    try:
        parsed = urlsplit(url)
        host = (parsed.hostname or "").lower().removesuffix(".").removeprefix("www.")
        # Treat any number of trailing slashes consistently so a Render URL
        # such as /deploy// cannot bypass deploy-button validation.
        path = parsed.path.rstrip("/")
    except ValueError as error:
        # The documentation URL sweep can truncate bracketed IPv6 examples at
        # the Markdown closing bracket. Ignore malformed unrelated URLs, but
        # keep malformed deployment-host candidates visible.
        malformed_candidate = re.match(
            r"^https?://\[?(?:www\.)?(?:render\.com|railway\.(?:com|app))(?:[/:?#]|$)",
            url,
            re.IGNORECASE,
        )
        if malformed_candidate:
            fail(f"{label} contains an invalid URL {url!r}: {error}")
        return None

    if host == "render.com" and path == "/deploy":
        if parsed.scheme.lower() != "https" or parsed.netloc.lower() != "render.com" or parsed.fragment:
            fail(f"{label} Render deploy URL must use canonical HTTPS with no credentials, port, or fragment: {url}")
        query = parse_qsl(parsed.query, keep_blank_values=True)
        if len(query) != 1 or query[0][0] != "repo" or not query[0][1]:
            fail(f"{label} Render deploy URL must contain exactly one non-empty repo parameter: {url}")

        repo_url = query[0][1]
        try:
            repo = urlsplit(repo_url)
        except ValueError as error:
            fail(f"{label} contains an invalid Render repo URL {repo_url!r}: {error}")
        if (
            repo.scheme.lower() != "https"
            or (repo.hostname or "").lower() != "github.com"
            or repo.netloc.lower() != "github.com"
            or repo.query
            or repo.fragment
            or not re.fullmatch(r"/[^/]+/[^/]+/tree/.+", repo.path)
        ):
            fail(f"{label} Render repo parameter must be an unambiguous GitHub branch URL: {repo_url}")
        return ("render", repo.path)

    if host in {"railway.com", "railway.app"} and (
        path == "/new/template" or path.startswith("/new/template/")
    ):
        # Recognize slug URLs, the historical query form, and a bare launch
        # path as deployment candidates. Query/fragment forms are rejected as
        # noncanonical below; a bare path is rejected for its missing slug.
        if (
            parsed.scheme.lower() != "https"
            or parsed.netloc.lower() != host
            or parsed.query
            or parsed.fragment
        ):
            fail(f"{label} Railway deploy URL must use canonical HTTPS with no credentials, port, query, or fragment: {url}")
        slug = path.removeprefix("/new/template").removeprefix("/")
        if not slug or "/" in slug:
            fail(f"{label} Railway deploy URL must name exactly one template slug in its path: {url}")
        return ("railway", slug)

    return None


def document_url_from_match(text: str, match: re.Match[str]) -> str:
    """Return the URL represented by a documentation match.

    A terminal period or similar character is sentence punctuation for a bare
    prose URL, but it is part of the destination when it appears inside a
    Markdown link, reference definition, autolink, or href/src attribute. Keep
    structured destinations byte-for-byte so validation stays bound to what a
    click actually sends to the deployment platform.
    """
    url = match.group(0)
    line_start = text.rfind("\n", 0, match.start()) + 1
    line_prefix = text[line_start : match.start()]
    structured_destination = (
        re.search(r"\]\(\s*<?$", line_prefix) is not None
        or re.search(r"^\s*\[[^\]]+\]:\s*<?$", line_prefix) is not None
        or re.search(r"\b(?:href|src)\s*=\s*[\"']$", line_prefix, re.IGNORECASE) is not None
        or line_prefix.endswith("<")
    )
    return url if structured_destination else url.rstrip(TRAILING_PUNCTUATION)


def document_deploy_buttons(path: Path) -> list[tuple[str, DeployButtonKey]]:
    """Return every one-click deploy URL in a documentation page."""
    label = str(path.relative_to(REPO_ROOT))
    buttons: list[tuple[str, DeployButtonKey]] = []
    text = CODE_SPAN_PATTERN.sub(" ", path.read_text())
    for match in HTTP_URL_PATTERN.finditer(text):
        url = document_url_from_match(text, match)
        key = parse_deploy_button_url(url, label)
        if key is not None:
            buttons.append((url, key))
    return buttons


def deployment_document_paths() -> list[Path]:
    """Every deployment guide, including future nested platform pages."""
    paths = sorted((REPO_ROOT / "docs/deployment-guides").rglob("*.mdx"))
    navigation = json.loads((REPO_ROOT / "docs/docs.json").read_text())["navigation"]

    def navigation_entries(value: Any) -> set[str]:
        entries: set[str] = set()
        if isinstance(value, str):
            entries.add(value)
        if isinstance(value, list):
            for item in value:
                entries.update(navigation_entries(item))
        if isinstance(value, dict):
            entries.update(navigation_entries(value.get("pages", [])))
        return entries

    top_level_entries = []
    if isinstance(navigation, list):
        top_level_entries = navigation
    elif isinstance(navigation, dict):
        top_level_entries = [
            entry
            for value in navigation.values()
            if isinstance(value, list)
            for entry in value
        ]
    deployment_groups = [
        entry
        for entry in top_level_entries
        if isinstance(entry, dict)
        and (entry.get("group") == "Deployment Guides" or entry.get("tab") == "Deployment Guides")
    ]
    if len(deployment_groups) != 1:
        fail(
            "docs/docs.json navigation must contain exactly one top-level Deployment Guides group, "
            f"found {len(deployment_groups)}"
        )

    documented = navigation_entries(deployment_groups[0].get("pages", []))
    missing = [
        path.relative_to(REPO_ROOT).as_posix()
        for path in paths
        if path.relative_to(REPO_ROOT / "docs").with_suffix("").as_posix() not in documented
    ]
    docs_root = (REPO_ROOT / "docs").resolve()
    stale = []
    for page in sorted(documented):
        target = (docs_root / f"{page}.mdx").resolve()
        if not target.is_relative_to(docs_root) or not target.is_file():
            stale.append(page)

    errors = []
    if missing:
        errors.append(f"docs/docs.json navigation is missing deployment guide {', '.join(missing)}")
    if stale:
        errors.append(
            "docs/docs.json Deployment Guides navigation references missing page "
            + ", ".join(stale)
        )
    if errors:
        fail("; ".join(errors))
    return paths


def validate_render_blueprint_reference(name: str, blueprint: str, label: str) -> Path:
    """Bind each verification slot to the Blueprint it is allowed to certify."""
    expected = RENDER_BLUEPRINT_PATHS[name]
    if blueprint != expected:
        fail(f"{label} must name {expected!r}, got {blueprint!r}")
    path = REPO_ROOT / expected
    if not path.is_file():
        fail(f"{label} expected blueprint {expected!r} is missing")
    return path


def verification_recorded(record: dict[str, Any], label: str) -> bool:
    """Whether `record` carries a complete, well-formed verification.

    Nothing in this repository can inspect a live Render Blueprint or Railway
    template, so a published button rests entirely on what is written here. That
    makes the shape of the record the only thing standing between a real
    deployment and a claim about one, and a truthiness test is not enough: it
    reads "soon" as a date and accepts an entry naming no release at all.

    A half-filled or malformed record fails rather than quietly counting as
    unverified. It was written by someone recording a verification, so treating
    it as an absent one would hide the mistake and, through the both-directions
    check below, report it as a missing documentation link instead.
    """
    last_verified = record.get("last_verified")
    verified_release = record.get("verified_release")

    if last_verified is None and verified_release is None:
        return False
    if last_verified is None or verified_release is None:
        fail(
            f"{label} records half a verification: last_verified and verified_release "
            f"are set together or not at all"
        )

    try:
        date.fromisoformat(last_verified)
    except ValueError:
        fail(f"{label} last_verified must be an ISO-8601 date, got {last_verified!r}")

    version = release_tuple(verified_release)
    if version is None:
        fail(f"{label} verified_release must be a release tag like {MINIMUM_RELEASE_TAG}, got {verified_release!r}")
    if version < release_tuple(MINIMUM_RELEASE_TAG):
        fail(
            f"{label} verified_release {verified_release} predates {MINIMUM_RELEASE_TAG}, the release "
            f"that carries the deployment runtime contract. Re-verify against a qualified release."
        )
    return True


def validate_json_schema(instance: Any, schema: dict[str, Any], label: str, draft: type) -> None:
    validator = draft(schema, format_checker=FormatChecker())
    errors = sorted(validator.iter_errors(instance), key=lambda error: list(error.absolute_path))
    if errors:
        details = "\n".join(
            f"  - {'.'.join(str(part) for part in error.absolute_path) or '<root>'}: {error.message}"
            for error in errors
        )
        fail(f"{label} failed schema validation:\n{details}")


def assert_bootstrap_config(config: dict[str, Any], label: str, storage: str) -> None:
    validate_json_schema(config, BIFROST_SCHEMA, f"{label} BIFROST_CONFIG", Draft201909Validator)
    if config.get("source_of_truth") != "split":
        fail(f"{label} must preserve dashboard-managed configuration with source_of_truth=split")
    if config.get("encryption_key") != "env.BIFROST_ENCRYPTION_KEY":
        fail(f"{label} must reference the generated encryption key")
    if config.get("client", {}).get("enforce_auth_on_inference") is not True:
        fail(f"{label} must reject anonymous inference")

    auth = config.get("governance", {}).get("auth_config", {})
    expected_auth = {
        "admin_username": "admin",
        "admin_password": "env.BIFROST_ADMIN_PASSWORD",
        "is_enabled": True,
    }
    if auth != expected_auth:
        fail(f"{label} must provision operator-managed dashboard authentication")

    for store_name in ("config_store", "logs_store"):
        store = config.get(store_name)
        if storage == "postgres":
            if not store or store.get("type") != "postgres" or store.get("enabled") is not True:
                fail(f"{label} must enable PostgreSQL for {store_name}")
            store_config = store.get("config", {})
            if store_config.get("ssl_mode") != "require":
                fail(f"{label} must require TLS for {store_name}")
            if store_config.get("max_open_conns", 0) > 20:
                fail(f"{label} exceeds the one-click PostgreSQL connection budget")
        elif store is not None:
            fail(f"{label} SQLite configuration must not override {store_name}")


def env_vars(service: dict[str, Any]) -> dict[str, dict[str, Any]]:
    values = service.get("envVars", [])
    return {entry["key"]: entry for entry in values}


def validate_render(path: Path, storage: str) -> None:
    blueprint = yaml.safe_load(path.read_text())
    services = blueprint.get("services", [])
    if len(services) != 1:
        fail(f"{path} must define exactly one Bifrost service")
    service = services[0]
    label = str(path.relative_to(REPO_ROOT))

    if service.get("runtime") != "image" or service.get("image", {}).get("url") != "docker.io/maximhq/bifrost:latest":
        fail(f"{label} must use the public latest image")
    if service.get("numInstances") != 1:
        fail(f"{label} must run one OSS replica")
    if service.get("autoDeploy") is not False:
        fail(f"{label} image-tag updates must require an intentional manual deploy")
    if service.get("healthCheckPath") != "/health":
        fail(f"{label} must use /health")
    if service.get("maxShutdownDelaySeconds") != 60:
        fail(f"{label} must allow 60 seconds for shutdown")

    variables = env_vars(service)
    for name in ("BIFROST_ENCRYPTION_KEY", "BIFROST_ADMIN_PASSWORD"):
        if variables.get(name, {}).get("generateValue") is not True:
            fail(f"{label} must generate {name}")
    config = json.loads(variables["BIFROST_CONFIG"]["value"])
    assert_bootstrap_config(config, label, storage)

    if storage == "postgres":
        databases = blueprint.get("databases", [])
        if len(databases) != 1:
            fail(f"{label} must define one PostgreSQL database")
        database = databases[0]
        if service.get("plan") != "free" or database.get("plan") != "free":
            fail(f"{label} PostgreSQL evaluation must use free plans")
        if database.get("postgresMajorVersion") != "18":
            fail(f"{label} must provision PostgreSQL 18")
        if database.get("ipAllowList") != []:
            fail(f"{label} database must not allow public network ranges")
        if service.get("disk") is not None:
            fail(f"{label} PostgreSQL service must remain ephemeral")
    else:
        disk = service.get("disk", {})
        if service.get("plan") != "starter":
            fail(f"{label} SQLite service must use the minimum disk-capable plan")
        if disk != {"name": "bifrost-data", "mountPath": "/app/data", "sizeGB": 1}:
            fail(f"{label} must attach a 1 GB /app/data disk")
        if blueprint.get("databases"):
            fail(f"{label} SQLite Blueprint must not provision PostgreSQL")


def railway_service(contract: dict[str, Any], name: str) -> dict[str, Any]:
    for service in contract["services"]:
        if service["name"] == name:
            return service
    fail(f"Railway contract does not contain {name}")


def validate_railway(path: Path, storage: str) -> None:
    contract = json.loads(path.read_text())
    label = str(path.relative_to(REPO_ROOT))
    validate_json_schema(contract, RAILWAY_SCHEMA, label, Draft202012Validator)
    bifrost = railway_service(contract, "Bifrost")

    if contract["template"]["owner"] != "Maxim AI's Projects":
        fail(f"{label} must remain Maxim-owned")

    if bifrost["image"] != "docker.io/maximhq/bifrost:latest" or bifrost.get("image_auto_update") != "daily":
        fail(f"{label} must track latest with daily image checks")
    deploy = bifrost["deploy"]
    expected = {"replicas": 1, "healthcheck_path": "/health", "draining_seconds": 60, "public_port": 8080}
    for key, value in expected.items():
        if deploy.get(key) != value:
            fail(f"{label} has invalid Bifrost deploy setting {key}")

    variables = bifrost["variables"]
    if variables.get("BIFROST_CONFIG", {}).get("sensitive") is not False:
        fail(f"{label} BIFROST_CONFIG must contain references, not secret values")
    for name in ("BIFROST_ENCRYPTION_KEY", "BIFROST_ADMIN_PASSWORD"):
        variable = variables.get(name, {})
        if variable.get("value") != "${{secret()}}" or variable.get("sensitive") is not True:
            fail(f"{label} must generate and hide {name}")
    config = json.loads(variables["BIFROST_CONFIG"]["value"])
    assert_bootstrap_config(config, label, storage)

    if storage == "postgres":
        postgres = railway_service(contract, "Postgres")
        if contract["template"]["slug"] != "blue-dark":
            fail(f"{label} must describe the existing blue-dark template")
        if postgres["image"] != "ghcr.io/railwayapp-templates/postgres-ssl:18":
            fail(f"{label} must provision the TLS-enabled PostgreSQL 18 image")
        expected_postgres_variables = {
            "POSTGRES_PASSWORD": "${{secret()}}",
            "PGHOST": "${{RAILWAY_PRIVATE_DOMAIN}}",
            "PGPORT": "5432",
            "PGUSER": "${{POSTGRES_USER}}",
            "PGPASSWORD": "${{POSTGRES_PASSWORD}}",
            "PGDATABASE": "${{POSTGRES_DB}}",
        }
        for name, value in expected_postgres_variables.items():
            if postgres["variables"].get(name, {}).get("value") != value:
                fail(f"{label} has invalid PostgreSQL variable {name}")
        if postgres["volumes"] != [{"mount_path": "/var/lib/postgresql/data"}]:
            fail(f"{label} must persist PostgreSQL data")
        if bifrost["volumes"]:
            fail(f"{label} PostgreSQL Bifrost service must not attach a volume")
        for variable in ("RAILWAY_RUN_UID", "BIFROST_RUN_AS_UID", "BIFROST_RUN_AS_GID"):
            if variable in variables:
                fail(f"{label} PostgreSQL Bifrost service must not set {variable}")
    else:
        expected_variables = {"RAILWAY_RUN_UID": "0", "BIFROST_RUN_AS_UID": "1000", "BIFROST_RUN_AS_GID": "0"}
        for name, value in expected_variables.items():
            if variables.get(name, {}).get("value") != value:
                fail(f"{label} must set {name}={value}")
        if bifrost["volumes"] != [{"mount_path": "/app/data"}]:
            fail(f"{label} must attach only /app/data")
        if deploy.get("overlap_seconds") != 0:
            fail(f"{label} must disable overlap for the single-writer SQLite volume")


def one_click_targets() -> list[dict[str, Any]]:
    """Every one-click button, paired with the evidence that it may be published.

    A live button is a public claim that the artifact behind it was deployed and
    checked. Nothing in this repository can inspect a live Render Blueprint or
    Railway template, so the claim rests on a recorded verification — and a
    button whose record is empty must not be advertised. The relationship is
    enforced in both directions so that verifying a template and publishing its
    button cannot drift apart.
    """
    render_evidence = "deploy/render/blueprint-verification.json"
    render = json.loads((REPO_ROOT / render_evidence).read_text())
    validate_json_schema(render, RENDER_VERIFICATION_SCHEMA, render_evidence, Draft202012Validator)
    render_docs = REPO_ROOT / "docs/deployment-guides/platforms/render.mdx"
    railway_docs = REPO_ROOT / "docs/deployment-guides/platforms/railway.mdx"

    targets: list[dict[str, Any]] = []
    if set(render["blueprints"]) != set(RENDER_BLUEPRINT_PATHS):
        fail(
            f"{render_evidence} blueprints must be exactly "
            f"{', '.join(sorted(RENDER_BLUEPRINT_PATHS))}"
        )
    for name, entry in render["blueprints"].items():
        label = f"Render {name}"
        # The record must be internally consistent before it can vouch for a
        # button: each slot is bound to its exact canonical Blueprint, and the
        # button must deploy this repository at the branch the record claims
        # was verified.
        validate_render_blueprint_reference(name, entry["blueprint"], f"{label} in {render_evidence}")
        validate_render_branch(entry["branch"], f"{label} in {render_evidence}")
        expected_button_url = (
            f"https://render.com/deploy?repo=https://github.com/maximhq/bifrost/tree/{entry['branch']}"
        )
        expected_button_key = parse_deploy_button_url(expected_button_url, label)
        button_key = (
            parse_deploy_button_url(entry["button_url"], f"{label} in {render_evidence}")
            if entry["button_url"]
            else None
        )
        if entry["button_url"] and button_key != expected_button_key:
            fail(
                f"{label} in {render_evidence} button_url must deploy the recorded branch of "
                f"maximhq/bifrost, expected {expected_button_url}, got {entry['button_url']}"
            )
        # Evaluated before the button URL is considered: a malformed record is a
        # mistake worth reporting whether or not its button is published yet.
        recorded = verification_recorded(entry, f"{label} in {render_evidence}")
        targets.append(
            {
                "label": label,
                "doc": render_docs,
                "url": entry["button_url"],
                "key": button_key,
                # A missing button URL means there is no link to publish, and an
                # empty one is a substring of every document — treating it as
                # verified would satisfy the link check below against nothing.
                "verified": bool(entry["button_url"]) and recorded,
                "evidence": render_evidence,
            }
        )

    for name, evidence in RAILWAY_TEMPLATE_CONTRACTS.values():
        template = json.loads((REPO_ROOT / evidence).read_text())["template"]
        slug = template["slug"]
        recorded = verification_recorded(template, f"Railway {name} in {evidence}")
        url = f"https://railway.com/new/template/{slug}" if slug else None
        targets.append(
            {
                "label": f"Railway {name}",
                "doc": railway_docs,
                "url": url,
                "key": parse_deploy_button_url(url, f"Railway {name} in {evidence}") if url else None,
                # An unassigned slug means the template is not published at all,
                # so it can never be verified regardless of what is recorded.
                "verified": bool(slug) and recorded,
                "evidence": evidence,
            }
        )
    return targets


def validate_documentation_links() -> None:
    targets = one_click_targets()
    buttons_by_doc = {path: document_deploy_buttons(path) for path in deployment_document_paths()}
    for target in targets:
        doc_label = str(target["doc"].relative_to(REPO_ROOT))
        if target["doc"] not in buttons_by_doc:
            fail(f"{target['label']} expects deployment page {doc_label}, but it is missing")
        doc_button_keys = {key for _, key in buttons_by_doc[target["doc"]]}
        if target["verified"]:
            if target["key"] not in doc_button_keys:
                fail(f"{target['label']} is verified in {target['evidence']} but {doc_label} does not link {target['url']}")
        elif target["key"] is not None and target["key"] in doc_button_keys:
            fail(
                f"{doc_label} publishes the {target['label']} one-click button while "
                f"{target['evidence']} records no verification. Verify the live template and record "
                f"last_verified and verified_release, or remove the button."
            )

    # Sweep for deploy-button URLs the exact-match checks above cannot see:
    # any render.com/deploy or Railway template link in the deployment docs
    # must be the URL of a verified target. This also covers targets whose
    # recorded URL is None (an unassigned Railway slug), which the per-target
    # check skips entirely.
    verified_keys = {target["key"] for target in targets if target["verified"]}
    overview_path = REPO_ROOT / "docs/deployment-guides/overview.mdx"
    for doc in deployment_document_paths():
        doc_label = str(doc.relative_to(REPO_ROOT))
        for url, key in buttons_by_doc[doc]:
            if key not in verified_keys:
                fail(
                    f"{doc_label} links the one-click deploy URL {url}, which no "
                    f"recorded verification covers. Verify the live template and record it, "
                    f"or remove the link."
                )

    # The overview advertises the hosted one-click choices as a group. It may do
    # so only while at least one of them is actually published.
    overview = overview_path.read_text()
    verified = [target["label"] for target in targets if target["verified"]]
    if verified and HOSTED_ONE_CLICK_HEADING not in overview:
        fail(f"docs/deployment-guides/overview.mdx must list the published one-click choices ({', '.join(verified)})")
    if not verified and HOSTED_ONE_CLICK_HEADING in overview:
        fail(
            "docs/deployment-guides/overview.mdx advertises hosted one-click choices while no template "
            "is verified. Remove the section until a button is published."
        )


def main() -> int:
    # The floor is hand-edited and shared with the release gate, which reads the
    # same file. A malformed value here would otherwise surface as a comparison
    # TypeError from deep inside a verification check.
    if release_tuple(MINIMUM_RELEASE_TAG) is None:
        fail(
            f"deploy/runtime-contract.json minimum_release_tag must be a release tag "
            f"like v1.6.12, got {MINIMUM_RELEASE_TAG!r}"
        )

    for storage, blueprint in RENDER_BLUEPRINT_PATHS.items():
        validate_render(REPO_ROOT / blueprint, storage)
    for storage, (_, contract) in RAILWAY_TEMPLATE_CONTRACTS.items():
        validate_railway(REPO_ROOT / contract, storage)
    validate_documentation_links()
    print("deployment template validation passed")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (AssertionError, KeyError, TypeError, ValueError, json.JSONDecodeError) as error:
        print(f"deployment template validation failed: {error}", file=sys.stderr)
        sys.exit(1)
