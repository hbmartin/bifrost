#!/usr/bin/env python3

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from collections.abc import Iterable
from pathlib import Path
from typing import Any


def digest(parts: Iterable[object]) -> str:
    value = "\0".join(str(part) for part in parts)
    return hashlib.sha256(value.encode()).hexdigest()


def decode_json_stream(text: str) -> list[Any]:
    decoder = json.JSONDecoder()
    documents: list[Any] = []
    position = 0
    while position < len(text):
        while position < len(text) and text[position].isspace():
            position += 1
        if position == len(text):
            break
        document, position = decoder.raw_decode(text, position)
        documents.append(document)
    return documents


def load_documents(path: Path, kind: str) -> list[Any]:
    files = sorted(path.glob(f"{kind}-*.json")) if path.is_dir() else [path]
    documents: list[Any] = []
    for file in files:
        if not file.exists() or not file.read_text().strip():
            continue
        documents.extend(decode_json_stream(file.read_text()))
    return documents


def normalize(kind: str, documents: list[Any]) -> list[str]:
    fingerprints: set[str] = set()
    for document in documents:
        if kind == "gitleaks":
            for finding in document if isinstance(document, list) else []:
                fingerprints.add(
                    finding.get("Fingerprint")
                    or digest([finding.get("RuleID"), finding.get("File"), finding.get("Commit")])
                )
        elif kind == "gosec":
            for finding in document.get("Issues", []) if isinstance(document, dict) else []:
                file = str(finding.get("file", "")).replace("\\", "/")
                fingerprints.add(
                    digest(
                        [
                            finding.get("rule_id"),
                            file.split("/bifrost/")[-1],
                            finding.get("details"),
                            finding.get("code"),
                        ]
                    )
                )
        elif kind == "govulncheck" and isinstance(document, dict) and "finding" in document:
            finding = document["finding"]
            trace = finding.get("trace") or [{}]
            frames = [frame for frame in trace if frame.get("function")]
            if not frames:
                continue
            frame = frames[0]
            fingerprints.add(
                digest(
                    [
                        finding.get("osv"),
                        frame.get("module"),
                        frame.get("package"),
                        frame.get("function"),
                    ]
                )
            )
    return sorted(fingerprints)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kind", choices=["gitleaks", "gosec", "govulncheck"], required=True)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--baseline", type=Path, required=True)
    parser.add_argument("--write", action="store_true")
    args = parser.parse_args()

    current = normalize(args.kind, load_documents(args.input, args.kind))
    baseline = json.loads(args.baseline.read_text()) if args.baseline.exists() else {}
    if args.write:
        baseline[args.kind] = current
        args.baseline.write_text(json.dumps(baseline, indent=2, sort_keys=True) + "\n")
        return 0

    accepted = set(baseline.get(args.kind, []))
    new = sorted(set(current) - accepted)
    if new:
        print(f"{args.kind}: {len(new)} new or changed finding(s)", file=sys.stderr)
        print("\n".join(new), file=sys.stderr)
        return 1
    print(f"{args.kind}: {len(current)} current finding(s), {len(accepted)} accepted baseline(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
