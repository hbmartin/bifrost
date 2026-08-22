#!/usr/bin/env python3
"""Regression tests for deployment button validation boundaries."""

from __future__ import annotations

import importlib.util
import unittest
from pathlib import Path


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


class DeploymentDocumentationTests(unittest.TestCase):
    def test_sweep_is_recursive(self) -> None:
        relative_paths = {
            path.relative_to(validator.REPO_ROOT).as_posix()
            for path in validator.deployment_document_paths()
        }
        self.assertIn("docs/deployment-guides/runtime-contract.mdx", relative_paths)
        self.assertIn("docs/deployment-guides/platforms/render.mdx", relative_paths)


if __name__ == "__main__":
    unittest.main()
