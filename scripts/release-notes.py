#!/usr/bin/env python3
"""Extract this version's notes instead of publishing the entire changelog."""

import argparse
from pathlib import Path
import re
import release


def notes(tag):
    base = release.read_version()
    version, channel = release.build_version(base, tag)
    text = (release.ROOT / "CHANGELOG.md").read_text()
    match = re.search(rf"^## {re.escape(base)}(?: .*)?\n(.*?)(?=^## |\Z)", text, re.M | re.S)
    if match is None:
        raise ValueError("current base version needs a CHANGELOG section")
    return (
        f"# Linkport {version}\n\nChannel: **{channel}**. "
        f"Commit: `{release.run('git', 'rev-parse', 'HEAD')}`.\n\n"
        "Downloads and installation: https://linkport.grezl.link/#download\n\n"
        + match.group(1).strip() + "\n"
    )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    args.output.write_text(notes(args.tag), encoding="utf-8")
