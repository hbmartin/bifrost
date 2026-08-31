#!/usr/bin/env python3

from __future__ import annotations

import argparse
import json
import sys
from collections import Counter
from pathlib import Path


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument("baseline", type=Path)
    parser.add_argument("--package", choices=("ui",))
    args = parser.parse_args()

    diagnostics = json.loads(args.results.read_text()).get("diagnostics", [])
    ceilings = json.loads(args.baseline.read_text())
    failures: list[str] = []
    counts: Counter[tuple[str, str]] = Counter()
    prefixes = sorted(ceilings, key=len, reverse=True)

    for diagnostic in diagnostics:
        severity = diagnostic.get("severity")
        filename = diagnostic.get("filename", "")
        rule = diagnostic.get("code", "")
        if severity == "error":
            failures.append(f"{filename}: {rule}: {diagnostic.get('message')}")
            continue
        package = args.package or next(
            (
                prefix
                for prefix in prefixes
                if filename == prefix or filename.startswith(prefix + "/")
            ),
            None,
        )
        if package is None or rule != "typescript-eslint(no-explicit-any)":
            failures.append(f"unbaselined warning: {filename}: {rule}")
            continue
        counts[(package, "typescript/no-explicit-any")] += 1

    for (package, rule), count in sorted(counts.items()):
        ceiling = ceilings[package][rule]
        print(f"{package}: {rule} {count}/{ceiling}")
        if count > ceiling:
            failures.append(f"{package}: {rule} warning ceiling exceeded ({count}>{ceiling})")

    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
