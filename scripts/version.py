#!/usr/bin/env python3
"""Bump the shared base version; publication still requires a reviewed Git tag."""

import argparse
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[1]


def bump(part):
    current = (ROOT / "VERSION").read_text().strip()
    if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", current):
        raise ValueError("VERSION is not a base semantic version")
    numbers = list(map(int, current.split(".")))
    index = ("major", "minor", "patch").index(part)
    numbers[index] += 1
    numbers[index + 1:] = [0] * (2 - index)
    version = ".".join(map(str, numbers))
    source_path = ROOT / "internal/version/version.go"
    source = source_path.read_text()
    source, count = re.subn(r'\bVersion\s*=\s*"[^"]+"', f'Version = "{version}-dev"', source)
    if count != 1:
        raise ValueError("expected one Go version default")
    package_path = ROOT / "website/package.json"
    package = json.loads(package_path.read_text())
    package["version"] = version
    source_path.write_text(source)
    package_path.write_text(json.dumps(package, indent=2, ensure_ascii=False) + "\n")
    (ROOT / "VERSION").write_text(version + "\n")
    changelog = ROOT / "CHANGELOG.md"
    text = changelog.read_text()
    changelog.write_text(text.replace("# Changelog\n", f"# Changelog\n\n## {version} — unreleased\n\n", 1))
    print(f"{current} -> {version}; review CHANGELOG.md, commit, then tag v{version}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("part", choices=("major", "minor", "patch"))
    bump(parser.parse_args().part)
