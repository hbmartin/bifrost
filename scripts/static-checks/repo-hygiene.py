#!/usr/bin/env python3

from __future__ import annotations

import os
import stat
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def repository_files() -> list[str]:
    output = subprocess.check_output(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
        cwd=ROOT,
    )
    return sorted(
        item.decode()
        for item in output.split(b"\0")
        if item and ((ROOT / item.decode()).exists() or (ROOT / item.decode()).is_symlink())
    )


def main() -> int:
    errors: list[str] = []
    files = repository_files()
    folded: dict[str, str] = {}
    for relative in files:
        path = ROOT / relative
        key = relative.casefold()
        if key in folded and folded[key] != relative:
            errors.append(f"case-conflicting paths: {folded[key]} and {relative}")
        folded[key] = relative
        if any(ord(char) < 32 or char in {"\\", ":"} for char in relative):
            errors.append(f"non-portable filename: {relative!r}")
        if path.is_symlink():
            target = (path.parent / os.readlink(path)).resolve()
            try:
                target.relative_to(ROOT)
            except ValueError:
                errors.append(f"symlink escapes repository: {relative}")
            continue
        if not path.exists():
            continue
        data = path.read_bytes()
        lines = data.splitlines()
        conflict_start = b"<" * 7 + b" "
        conflict_end = b">" * 7 + b" "
        if (
            any(line.startswith(conflict_start) for line in lines)
            and b"=======" in lines
            and any(line.startswith(conflict_end) for line in lines)
        ):
            errors.append(f"merge-conflict marker: {relative}")
        executable = bool(path.stat().st_mode & stat.S_IXUSR)
        has_shebang = data.startswith(b"#!")
        is_binary = b"\0" in data[:8192]
        if executable and not has_shebang and not is_binary:
            errors.append(f"executable file lacks shebang: {relative}")
        if has_shebang and not executable:
            errors.append(f"script with shebang is not executable: {relative}")
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print(f"repository hygiene passed for {len(files)} repository files")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
