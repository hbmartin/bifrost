#!/usr/bin/env python3
"""Regression tests for deployment button validation boundaries."""

from __future__ import annotations

import json
import importlib.util
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from jsonschema import Draft202012Validator


SCRIPT_PATH = Path(__file__).with_name("validate-deployment-templates.py")
SPEC = importlib.util.spec_from_file_location("deployment_template_validator", SCRIPT_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"could not load {SCRIPT_PATH}")
validator = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(validator)


class DeployButtonURLTests(unittest.TestCase):
    def test_render_host_matching_is_case_insensitive(self) -> None:
        key = validator.parse_deploy_button_url(
            "https://Render.com/deploy?repo=https://github.com/maximhq/bifrost/tree/dev",
            "test",
        )
        self.assertEqual(("render", "/maximhq/bifrost/tree/dev"), key)

    def test_render_fragment_cannot_change_the_effective_branch(self) -> None:
        with self.assertRaisesRegex(AssertionError, "no credentials, port, or fragment"):
            validator.parse_deploy_button_url(
                "https://render.com/deploy?repo=https://github.com/maximhq/bifrost/tree/dev#unverified",
                "test",
            )

    def test_render_repo_parameter_must_be_unique(self) -> None:
        with self.assertRaisesRegex(AssertionError, "exactly one"):
            validator.parse_deploy_button_url(
                "https://render.com/deploy?repo=https://github.com/maximhq/bifrost/tree/dev&repo=https://github.com/attacker/other/tree/dev",
                "test",
            )

    def test_different_repository_has_a_different_identity(self) -> None:
        expected = validator.parse_deploy_button_url(
            "https://render.com/deploy?repo=https://github.com/maximhq/bifrost/tree/dev",
            "test",
        )
        attacker = validator.parse_deploy_button_url(
            "https://render.com/deploy?repo=https://github.com/attacker/other/tree/dev",
            "test",
        )
        self.assertNotEqual(expected, attacker)

    def test_render_branch_must_be_safe_to_embed_verbatim(self) -> None:
        for branch in ("dev#unverified", "dev/../main", "dev//main", ".hidden"):
            with self.subTest(branch=branch):
                with self.assertRaisesRegex(AssertionError, "cannot be embedded safely"):
                    validator.validate_render_branch(branch, "test")

    def test_verification_cannot_point_at_an_arbitrary_repository_file(self) -> None:
        with self.assertRaisesRegex(AssertionError, "must name 'render.yaml'"):
            validator.validate_render_blueprint_reference("postgres", "README.md", "test")

    def test_railway_hosts_share_one_template_identity(self) -> None:
        railway_com = validator.parse_deploy_button_url(
            "https://railway.com/new/template/blue-dark", "test"
        )
        railway_app = validator.parse_deploy_button_url(
            "https://RAILWAY.app/new/template/blue-dark", "test"
        )
        self.assertEqual(railway_com, railway_app)

    def test_deploy_host_aliases_are_rejected_instead_of_ignored(self) -> None:
        urls = (
            "https://www.render.com/deploy?repo=https://github.com/maximhq/bifrost/tree/dev",
            "https://render.com./deploy?repo=https://github.com/maximhq/bifrost/tree/dev",
            "https://www.railway.com/new/template/blue-dark",
            "https://railway.com./new/template/blue-dark",
        )
        for url in urls:
            with self.subTest(url=url):
                with self.assertRaisesRegex(AssertionError, "canonical HTTPS"):
                    validator.parse_deploy_button_url(url, "test")

    def test_one_trailing_path_slash_preserves_deploy_identity(self) -> None:
        render = validator.parse_deploy_button_url(
            "https://render.com/deploy/?repo=https://github.com/maximhq/bifrost/tree/dev",
            "test",
        )
        railway = validator.parse_deploy_button_url(
            "https://railway.com/new/template/blue-dark/", "test"
        )
        self.assertEqual(("render", "/maximhq/bifrost/tree/dev"), render)
        self.assertEqual(("railway", "blue-dark"), railway)

    def test_repeated_trailing_slashes_cannot_bypass_render_validation(self) -> None:
        render = validator.parse_deploy_button_url(
            "https://render.com/deploy//?repo=https://github.com/maximhq/bifrost/tree/dev",
            "test",
        )
        self.assertEqual(("render", "/maximhq/bifrost/tree/dev"), render)

    def test_non_deploy_urls_are_ignored_by_host_and_path(self) -> None:
        urls = (
            "https://example.com/deploy?repo=https://github.com/maximhq/bifrost/tree/dev",
            "https://render.com/pricing",
        )
        for url in urls:
            with self.subTest(url=url):
                self.assertIsNone(validator.parse_deploy_button_url(url, "test"))

    def test_malformed_url_is_reported(self) -> None:
        with self.assertRaisesRegex(AssertionError, "contains an invalid URL"):
            validator.parse_deploy_button_url(
                "https://[render.com/deploy?repo=https://github.com/maximhq/bifrost/tree/dev",
                "test",
            )

    def test_malformed_unrelated_url_is_ignored(self) -> None:
        self.assertIsNone(
            validator.parse_deploy_button_url("https://[2001:db8::1", "test")
        )

    def test_bare_railway_template_path_is_rejected(self) -> None:
        with self.assertRaisesRegex(AssertionError, "must name exactly one template slug"):
            validator.parse_deploy_button_url(
                "https://railway.com/new/template",
                "test",
            )

    def test_bare_railway_template_path_with_fragment_is_rejected(self) -> None:
        with self.assertRaisesRegex(AssertionError, "no credentials, port, query, or fragment"):
            validator.parse_deploy_button_url(
                "https://railway.com/new/template#fragment",
                "test",
            )

    def test_railway_query_form_is_rejected_as_noncanonical(self) -> None:
        with self.assertRaisesRegex(AssertionError, "no credentials, port, query, or fragment"):
            validator.parse_deploy_button_url(
                "https://railway.com/new/template?template=blue-dark",
                "test",
            )


class RenderVerificationSchemaTests(unittest.TestCase):
    def test_button_url_uri_format_is_enforced(self) -> None:
        record = {
            "blueprints": {
                "postgres": {
                    "blueprint": "render.yaml",
                    "branch": "dev",
                    "button_url": "not a uri",
                    "last_verified": None,
                    "verified_release": None,
                }
            }
        }
        with self.assertRaisesRegex(AssertionError, "is not a 'uri'"):
            validator.validate_json_schema(
                record,
                validator.RENDER_VERIFICATION_SCHEMA,
                "test",
                Draft202012Validator,
            )

    def test_branch_pattern_matches_runtime_validation(self) -> None:
        branch_schema = validator.RENDER_VERIFICATION_SCHEMA["properties"]["blueprints"][
            "additionalProperties"
        ]["properties"]["branch"]
        self.assertEqual(validator.RENDER_BRANCH_PATTERN.pattern, branch_schema["pattern"])


class DeploymentDocumentationTests(unittest.TestCase):
    def test_bracketed_ipv6_example_is_ignored(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            guide = root / "docs/deployment-guides/platforms/networking.mdx"
            guide.parent.mkdir(parents=True)
            guide.write_text("Connect to https://[2001:db8::1]:8080/path.\n")

            with mock.patch.object(validator, "REPO_ROOT", root):
                self.assertEqual([], validator.document_deploy_buttons(guide))

    def test_malformed_deploy_candidate_is_not_silently_dropped(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            guide = root / "docs/deployment-guides/platforms/render.mdx"
            guide.parent.mkdir(parents=True)
            guide.write_text(
                "[Deploy](https://[render.com/deploy?repo=https://github.com/maximhq/bifrost/tree/dev)\n"
            )

            with mock.patch.object(validator, "REPO_ROOT", root):
                with self.assertRaisesRegex(AssertionError, "contains an invalid URL"):
                    validator.document_deploy_buttons(guide)

    def test_bare_url_sentence_punctuation_is_not_part_of_the_button(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            guide = root / "docs/deployment-guides/platforms/railway.mdx"
            guide.parent.mkdir(parents=True)
            guide.write_text(
                "Deploy from https://railway.com/new/template/blue-dark.\n"
            )

            with mock.patch.object(validator, "REPO_ROOT", root):
                self.assertEqual(
                    [
                        (
                            "https://railway.com/new/template/blue-dark",
                            ("railway", "blue-dark"),
                        )
                    ],
                    validator.document_deploy_buttons(guide),
                )

    def test_markdown_link_punctuation_remains_part_of_the_destination(self) -> None:
        cases = (
            (
                "https://railway.com/new/template/blue-dark.",
                ("railway", "blue-dark."),
            ),
            (
                "https://render.com/deploy?repo=https://github.com/maximhq/bifrost/tree/dev.",
                ("render", "/maximhq/bifrost/tree/dev."),
            ),
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            guide = root / "docs/deployment-guides/platforms/hosted.mdx"
            guide.parent.mkdir(parents=True)

            for url, expected_key in cases:
                with self.subTest(url=url):
                    guide.write_text(f"[Deploy]({url})\n")
                    with mock.patch.object(validator, "REPO_ROOT", root):
                        self.assertEqual(
                            [(url, expected_key)],
                            validator.document_deploy_buttons(guide),
                        )

    def test_sweep_is_recursive(self) -> None:
        relative_paths = {
            path.relative_to(validator.REPO_ROOT).as_posix()
            for path in validator.deployment_document_paths()
        }
        self.assertIn("docs/deployment-guides/runtime-contract.mdx", relative_paths)
        self.assertIn("docs/deployment-guides/platforms/render.mdx", relative_paths)

    def test_every_discovered_guide_must_be_in_navigation(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            guide = root / "docs/deployment-guides/platforms/future.mdx"
            guide.parent.mkdir(parents=True)
            guide.write_text("# Future deployment guide\n")
            (root / "docs/docs.json").write_text(
                json.dumps(
                    {
                        "navigation": {
                            "tabs": [{"tab": "Deployment Guides", "pages": []}]
                        }
                    }
                )
            )

            with mock.patch.object(validator, "REPO_ROOT", root):
                with self.assertRaisesRegex(
                    AssertionError,
                    "docs/docs.json navigation is missing deployment guide docs/deployment-guides/platforms/future.mdx",
                ):
                    validator.deployment_document_paths()

    def test_guide_registered_outside_deployment_group_is_still_missing(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            guide = root / "docs/deployment-guides/platforms/future.mdx"
            guide.parent.mkdir(parents=True)
            guide.write_text("# Future deployment guide\n")
            (root / "docs/docs.json").write_text(
                json.dumps(
                    {
                        "navigation": {
                            "tabs": [
                                {
                                    "tab": "Other",
                                    "pages": ["deployment-guides/platforms/future"],
                                },
                                {"tab": "Deployment Guides", "pages": []},
                            ]
                        }
                    }
                )
            )

            with mock.patch.object(validator, "REPO_ROOT", root):
                with self.assertRaisesRegex(
                    AssertionError,
                    "navigation is missing deployment guide docs/deployment-guides/platforms/future.mdx",
                ):
                    validator.deployment_document_paths()

    def test_stale_deployment_navigation_entry_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "docs/deployment-guides").mkdir(parents=True)
            (root / "docs/docs.json").write_text(
                json.dumps(
                    {
                        "navigation": {
                            "tabs": [
                                {
                                    "tab": "Deployment Guides",
                                    "pages": [
                                        {
                                            "group": "Hosted Containers",
                                            "pages": ["deployment-guides/platforms/missing"],
                                        }
                                    ],
                                }
                            ]
                        }
                    }
                )
            )

            with mock.patch.object(validator, "REPO_ROOT", root):
                with self.assertRaisesRegex(
                    AssertionError,
                    "Deployment Guides navigation references missing page deployment-guides/platforms/missing",
                ):
                    validator.deployment_document_paths()

    def test_missing_target_document_reports_validation_error(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            missing_doc = root / "docs/deployment-guides/platforms/render.mdx"
            target = {
                "label": "Render postgres",
                "doc": missing_doc,
                "url": "https://render.com/deploy?repo=https://github.com/maximhq/bifrost/tree/dev",
                "key": ("render", "/maximhq/bifrost/tree/dev"),
                "verified": True,
                "evidence": "deploy/render/blueprint-verification.json",
            }

            with (
                mock.patch.object(validator, "REPO_ROOT", root),
                mock.patch.object(validator, "one_click_targets", return_value=[target]),
                mock.patch.object(validator, "deployment_document_paths", return_value=[]),
            ):
                with self.assertRaisesRegex(
                    AssertionError,
                    "Render postgres expects deployment page docs/deployment-guides/platforms/render.mdx, but it is missing",
                ):
                    validator.validate_documentation_links()

    def test_document_deploy_button_without_verified_target_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            guide = root / "docs/deployment-guides/platforms/future.mdx"
            guide.parent.mkdir(parents=True)
            url = "https://railway.com/new/template/unverified"
            guide.write_text(f"[Deploy]({url})\n")

            with (
                mock.patch.object(validator, "REPO_ROOT", root),
                mock.patch.object(validator, "one_click_targets", return_value=[]),
                mock.patch.object(validator, "deployment_document_paths", return_value=[guide]),
            ):
                self.assertEqual(
                    [(url, ("railway", "unverified"))],
                    validator.document_deploy_buttons(guide),
                )
                with self.assertRaisesRegex(AssertionError, "no recorded verification covers"):
                    validator.validate_documentation_links()


if __name__ == "__main__":
    unittest.main()
