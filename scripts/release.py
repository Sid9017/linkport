#!/usr/bin/env python3
"""Build the native preview command and deterministic archives using stdlib only."""

import argparse
import datetime
import gzip
import hashlib
import io
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import unittest
from unittest import mock
import zipfile


ROOT = Path(__file__).resolve().parents[1]
MODULE = "github.com/Sid9017/linkport/internal/version"
TARGETS = tuple(
    (system, arch)
    for system in ("darwin", "linux", "windows")
    for arch in ("amd64", "arm64")
)


def run(*command, env=None):
    return subprocess.check_output(
        command, cwd=ROOT, env=env, text=True, encoding="utf-8"
    ).strip()


def read_version():
    version = (ROOT / "VERSION").read_text(encoding="utf-8").strip()
    if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version):
        raise ValueError("VERSION must be a plain major.minor.patch version")
    source = (ROOT / "internal/version/version.go").read_text(encoding="utf-8")
    default = re.search(r'\bVersion\s*=\s*"([^"]+)"', source)
    if default is None or default.group(1) != version + "-dev":
        raise ValueError("the Go default must be VERSION followed by -dev")
    return version


def validate_tag(tag, version):
    if tag != f"v{version}" and not re.fullmatch(
        rf"v{re.escape(version)}-nightly\.[0-9]{{14}}\.[1-9][0-9]*", tag
    ):
        raise ValueError(f"tag must be v{version} or a versioned nightly tag, got {tag!r}")
    tagged_commit = run("git", "rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}")
    if tagged_commit != run("git", "rev-parse", "HEAD"):
        raise ValueError("release tag must point to the checked-out commit")
    if run("git", "status", "--porcelain"):
        raise ValueError("tagged releases require a clean checkout")

def build_version(base, tag=None):
    if tag:
        validate_tag(tag, base)
        return tag[1:], "nightly" if "-nightly." in tag else "stable"
    commit = run("git", "rev-parse", "--short=12", "HEAD")
    dirty = ".dirty" if run("git", "status", "--porcelain") else ""
    return f"{base}-dev.{source_epoch()}.g{commit}{dirty}", "dev"


def source_epoch():
    value = os.environ.get("SOURCE_DATE_EPOCH")
    if value is None:
        value = run("git", "show", "-s", "--format=%ct", "HEAD")
    if not re.fullmatch(r"[0-9]+", value) or not 0 <= int(value) <= 4354819198:
        raise ValueError("SOURCE_DATE_EPOCH must be seconds from 1970 through 2107")
    return int(value)


def archive_name(version, target):
    system, arch = target
    extension = ".zip" if system == "windows" else ".tar.gz"
    return f"linkport_{version}_{system}_{arch}{extension}"


def write_archive(destination, entries, epoch):
    if destination.suffix == ".zip":
        timestamp = datetime.datetime.fromtimestamp(
            max(epoch, 315532800), datetime.timezone.utc
        ).timetuple()[:6]
        with zipfile.ZipFile(destination, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            for name, content, mode in sorted(entries):
                info = zipfile.ZipInfo(name, date_time=timestamp)
                info.create_system = 3
                info.external_attr = (0o100000 | mode) << 16
                info.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(info, content, compresslevel=9)
    else:
        with destination.open("wb") as output:
            with gzip.GzipFile(filename="", mode="wb", fileobj=output, mtime=epoch) as compressed:
                with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
                    for name, content, mode in sorted(entries):
                        info = tarfile.TarInfo(name)
                        info.size = len(content)
                        info.mode = mode
                        info.mtime = epoch
                        info.uid = info.gid = 0
                        info.uname = info.gname = ""
                        archive.addfile(info, io.BytesIO(content))


def write_checksums(output, version, require_all=False):
    archives = sorted(
        path for path in output.iterdir()
        if path.is_file() and (path.name.endswith(".tar.gz") or path.suffix == ".zip")
    )
    expected = {archive_name(version, target) for target in TARGETS}
    actual = {path.name for path in archives}
    if not actual or not actual <= expected:
        raise ValueError("output must contain only archives for the current VERSION")
    if require_all and actual != expected:
        raise ValueError(f"missing release archives: {', '.join(sorted(expected - actual))}")
    lines = []
    for archive in archives:
        with archive.open("rb") as stream:
            digest = hashlib.file_digest(stream, "sha256").hexdigest()
        lines.append(f"{digest}  {archive.name}\n")
    (output / "checksums.txt").write_text("".join(lines), encoding="utf-8", newline="\n")


def build(output, version, expected_target=None, channel="dev"):
    env = os.environ.copy()
    env["GOCACHE"] = os.path.abspath("/tmp/linkport-gocache")
    target = tuple(run("go", "env", "GOHOSTOS", "GOHOSTARCH", env=env).splitlines())
    if target not in TARGETS:
        raise ValueError(f"unsupported native platform: {target}")
    if expected_target and expected_target != "_".join(target):
        raise ValueError(f"expected native {expected_target}, got {'_'.join(target)}")
    if not (ROOT / "cmd/linkport").is_dir():
        raise ValueError("cmd/linkport is required; no placeholder binary will be packaged")
    system, arch = target
    env.update(GOOS=system, GOARCH=arch, CGO_ENABLED="1" if system == "darwin" else "0")
    epoch = source_epoch()
    date = datetime.datetime.fromtimestamp(epoch, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    commit = run("git", "rev-parse", "HEAD")
    if run("git", "status", "--porcelain"):
        commit += "-dirty"
    flags = (
        f"-s -w -buildid= -X {MODULE}.Version={version} "
        f"-X {MODULE}.Commit={commit} -X {MODULE}.Date={date} -X {MODULE}.Channel={channel}"
    )
    binary_name = "linkport.exe" if system == "windows" else "linkport"
    with tempfile.TemporaryDirectory(prefix="linkport-release-") as temporary:
        binary = Path(temporary) / binary_name
        subprocess.run(
            ["go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false",
             "-ldflags", flags, "-o", str(binary), "./cmd/linkport"],
            cwd=ROOT, env=env, check=True,
        )
        entries = [
            (binary_name, binary.read_bytes(), 0o755),
            ("VERSION", f"{version}\n".encode(), 0o644),
            ("CHANNEL", f"{channel}\n".encode(), 0o644),
            ("CHANGELOG.md", (ROOT / "CHANGELOG.md").read_bytes(), 0o644),
            ("README.md", (ROOT / "README.md").read_bytes(), 0o644),
            ("licenses/Geist-OFL.txt", (ROOT / "docs/design/assets/fonts/OFL.txt").read_bytes(), 0o644),
        ]
        destination = output / archive_name(version, target)
        write_archive(destination, entries, epoch)
    write_checksums(output, version)
    print(f"{destination}\ncommit={commit}\ndate={date}\ncgo={env['CGO_ENABLED']}")


def self_test():
    class PackagingTests(unittest.TestCase):
        def test_archives_are_repeatable_and_normalized(self):
            entries = [("linkport", b"preview\n", 0o755), ("VERSION", b"0.0.1\n", 0o644)]
            epoch = 1794787200
            with tempfile.TemporaryDirectory() as directory:
                for suffix in (".tar.gz", ".zip"):
                    first = Path(directory) / f"first{suffix}"
                    second = Path(directory) / f"second{suffix}"
                    write_archive(first, entries, epoch)
                    write_archive(second, list(reversed(entries)), epoch)
                    self.assertEqual(first.read_bytes(), second.read_bytes())
                    if suffix == ".zip":
                        with zipfile.ZipFile(first) as archive:
                            self.assertEqual(archive.namelist(), ["VERSION", "linkport"])
                            self.assertEqual(archive.read("linkport"), b"preview\n")
                            self.assertEqual(archive.getinfo("linkport").external_attr >> 16, 0o100755)
                    else:
                        with tarfile.open(first) as archive:
                            self.assertEqual(archive.getnames(), ["VERSION", "linkport"])
                            binary = archive.getmember("linkport")
                            self.assertEqual((binary.uid, binary.gid, binary.mode, binary.mtime),
                                             (0, 0, 0o755, epoch))
                            self.assertEqual(archive.extractfile(binary).read(), b"preview\n")

        def test_checksums_require_six_and_match_content(self):
            with tempfile.TemporaryDirectory() as directory:
                output = Path(directory)
                with self.assertRaises(ValueError):
                    write_checksums(output, "0.0.1", True)
                for target in TARGETS:
                    (output / archive_name("0.0.1", target)).write_bytes(b"preview")
                write_checksums(output, "0.0.1", True)
                lines = (output / "checksums.txt").read_text().splitlines()
                self.assertEqual(len(lines), 6)
                self.assertEqual(lines, sorted(lines, key=lambda line: line.split("  ")[1]))
                for line in lines:
                    digest, name = line.split("  ")
                    self.assertEqual(digest, hashlib.sha256((output / name).read_bytes()).hexdigest())
                (output / archive_name("0.0.1", TARGETS[0])).unlink()
                with self.assertRaises(ValueError):
                    write_checksums(output, "0.0.1", True)

        def test_wrong_version_archives_rejected(self):
            with tempfile.TemporaryDirectory() as directory:
                output = Path(directory)
                (output / "linkport_9.9.9_linux_amd64.tar.gz").write_bytes(b"stale")
                with self.assertRaises(ValueError):
                    write_checksums(output, "0.0.1")

        def test_tag_mismatch_rejected_before_git(self):
            for tag in ("v0.0.2", "0.0.1", "v0.0.1-rc.1", "v0.0.1\n"):
                with self.subTest(tag=tag), self.assertRaises(ValueError):
                    validate_tag(tag, "0.0.1")

        def test_tag_must_match_clean_head(self):
            with mock.patch(f"{__name__}.run", side_effect=["tagged", "other"]):
                with self.assertRaisesRegex(ValueError, "checked-out commit"):
                    validate_tag("v0.0.1", "0.0.1")
            with mock.patch(f"{__name__}.run", side_effect=["same", "same", " M VERSION"]):
                with self.assertRaisesRegex(ValueError, "clean checkout"):
                    validate_tag("v0.0.1", "0.0.1")
            with mock.patch(f"{__name__}.run", side_effect=["same", "same", ""]):
                validate_tag("v0.0.1", "0.0.1")

        def test_channels_have_different_versions(self):
            with mock.patch(f"{__name__}.validate_tag"):
                self.assertEqual(build_version("0.0.2", "v0.0.2"), ("0.0.2", "stable"))
                self.assertEqual(
                    build_version("0.0.2", "v0.0.2-nightly.20261117180000.1"),
                    ("0.0.2-nightly.20261117180000.1", "nightly"),
                )
            with mock.patch(f"{__name__}.run", side_effect=["012345abcdef", " M VERSION"]), \
                 mock.patch(f"{__name__}.source_epoch", return_value=1794913200):
                self.assertEqual(build_version("0.0.2"), ("0.0.2-dev.1794913200.g012345abcdef.dirty", "dev"))

        def test_wrong_native_platform_rejected(self):
            with mock.patch(f"{__name__}.run", return_value="darwin\narm64"):
                with self.assertRaisesRegex(ValueError, "expected native darwin_amd64"):
                    build(Path("unused"), "0.0.1", "darwin_amd64")

    result = unittest.TextTestRunner(verbosity=2).run(
        unittest.defaultTestLoader.loadTestsFromTestCase(PackagingTests)
    )
    return 0 if result.wasSuccessful() else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    action = parser.add_mutually_exclusive_group()
    action.add_argument("--validate-only", action="store_true")
    action.add_argument("--checksums-only", action="store_true")
    action.add_argument("--self-test", action="store_true")
    parser.add_argument("--tag", help="require an existing vVERSION tag at clean HEAD")
    parser.add_argument("--output", type=Path, default=ROOT / "dist")
    parser.add_argument("--target", choices=["_".join(target) for target in TARGETS],
                        help="assert the native Go host platform (no cross compilation)")
    parser.add_argument("--require-all", action="store_true", help="require all six archives")
    args = parser.parse_args()
    if args.self_test:
        return self_test()
    try:
        if args.require_all and not args.checksums_only:
            raise ValueError("--require-all requires --checksums-only")
        base = read_version()
        version, channel = build_version(base, args.tag)
        if args.validate_only:
            print(f"validated version {version}, channel {channel}" + (f", tag {args.tag}" if args.tag else ""))
            return 0
        output = args.output.resolve()
        output.mkdir(parents=True, exist_ok=True)
        if args.checksums_only:
            write_checksums(output, version, args.require_all)
        else:
            build(output, version, args.target, channel)
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"release: {error}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
