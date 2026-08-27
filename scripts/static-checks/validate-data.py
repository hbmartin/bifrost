#!/usr/bin/env python3

from __future__ import annotations

import json
import sys
import tomllib
from pathlib import Path

import yaml


def validate(path: Path) -> None:
    suffix = path.suffix.lower()
    with path.open("rb") as stream:
        if suffix == ".json":
            json.load(stream)
        elif suffix == ".toml":
            tomllib.load(stream)
        elif suffix in {".yaml", ".yml"}:
            list(yaml.safe_load_all(stream))


def main() -> int:
    failed = False
    for raw_path in sys.argv[1:]:
        path = Path(raw_path)
        if not path.exists():
            continue
        try:
            validate(path)
        except Exception as exc:
            print(f"{path}: {exc}", file=sys.stderr)
            failed = True
    return int(failed)


if __name__ == "__main__":
    raise SystemExit(main())
