#!/usr/bin/env python3
"""Build and package the native Wails application on the current host."""

import argparse
import datetime
import hashlib
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile
from urllib.parse import urlparse

import release


ROOT = Path(__file__).resolve().parents[1]
DESKTOP = ROOT / "desktop"
IDENTIFIER = "com.linkport.desktop"


def go_output(go, *arguments, env):
    return subprocess.check_output(
        [go, *arguments], cwd=DESKTOP, env=env, text=True
    ).strip()


def mac_bundle(directory, binary, version, env):
    app = directory / "Linkport.app"
    contents = app / "Contents"
    resources = contents / "Resources"
    executable = contents / "MacOS" / "linkport-desktop"
    executable.parent.mkdir(parents=True)
    resources.mkdir(parents=True)
    shutil.copy2(binary, executable)
    with (contents / "Info.plist").open("wb") as stream:
        plistlib.dump({
            "CFBundleIdentifier": IDENTIFIER,
            "CFBundleName": "Linkport",
            "CFBundleDisplayName": "Linkport",
            "CFBundleExecutable": "linkport-desktop",
            "CFBundlePackageType": "APPL",
            "CFBundleShortVersionString": version,
            "CFBundleVersion": version,
            "CFBundleIconFile": "Linkport",
            "LSMinimumSystemVersion": "14.5",
            "LSApplicationCategoryType": "public.app-category.developer-tools",
            "NSPrincipalClass": "NSApplication",
            "NSHighResolutionCapable": True,
            "NSAppTransportSecurity": {"NSAllowsLocalNetworking": True},
        }, stream)
    with tempfile.TemporaryDirectory(prefix="linkport-icons-") as temporary:
        iconset = Path(temporary) / "Linkport.iconset"
        iconset.mkdir()
        for size in (16, 32, 128, 256, 512):
            for scale in (1, 2):
                suffix = "@2x" if scale == 2 else ""
                target = iconset / f"icon_{size}x{size}{suffix}.png"
                subprocess.run([
                    "sips", "-z", str(size * scale), str(size * scale),
                    str(DESKTOP / "assets/app.png"), "--out", str(target),
                ], check=True, stdout=subprocess.DEVNULL)
        subprocess.run([
            "iconutil", "-c", "icns", str(iconset), "-o", str(resources / "Linkport.icns"),
        ], check=True)
    subprocess.run(["codesign", "--force", "--deep", "--sign", "-", str(app)],
                   check=True, env=env, stdout=subprocess.DEVNULL)
    return app


def build_executables(go, env, flags, binary):
    # Embed the matching CLI so executable-only desktop updates also update
    # the command. Keep Windows CLI in console mode (no -H windowsgui).
    cli_flags = flags.replace(" -H windowsgui", "")
    payload = DESKTOP / "assets/cli/linkport.bin"
    payload.parent.mkdir(parents=True, exist_ok=True)
    try:
        subprocess.run([
            go, "build", "-mod=readonly", "-trimpath", "-buildvcs=false",
            "-ldflags", cli_flags, "-o", str(payload), "./cmd/linkport",
        ], cwd=ROOT, env=env, check=True)
        subprocess.run([
            go, "build", "-mod=readonly", "-tags", "production,bundledcli",
            "-trimpath", "-buildvcs=false", "-ldflags", flags, "-o", str(binary), ".",
        ], cwd=DESKTOP, env=env, check=True)
    finally:
        payload.unlink(missing_ok=True)


def build(output, expected_target, go, tag):
    base = release.read_version()
    version, channel = release.build_version(base, tag)
    env = os.environ.copy()
    env.setdefault("GOTOOLCHAIN", "auto")
    env.setdefault("GOCACHE", str(Path(tempfile.gettempdir()) / "linkport-desktop-gocache"))
    target = tuple(go_output(go, "env", "GOHOSTOS", "GOHOSTARCH", env=env).splitlines())
    if target not in release.TARGETS:
        raise ValueError(f"unsupported native platform: {target}")
    if expected_target and expected_target != "_".join(target):
        raise ValueError(f"expected native {expected_target}, got {'_'.join(target)}")
    system, arch = target
    env.update(GOOS=system, GOARCH=arch, CGO_ENABLED="0" if system == "windows" else "1")
    if system == "darwin":
        env["MACOSX_DEPLOYMENT_TARGET"] = "14.5"
        for variable in ("CGO_CFLAGS", "CGO_CXXFLAGS"):
            env[variable] = env.get(variable, "") + " -mmacosx-version-min=14.5"
    epoch = release.source_epoch()
    commit = release.run("git", "rev-parse", "HEAD")
    if release.run("git", "status", "--porcelain"):
        commit += "-dirty"
    flags = (
        f"-s -w -buildid= -X {release.MODULE}.Version={version} "
        f"-X {release.MODULE}.Commit={commit} "
        f"-X {release.MODULE}.Channel={channel} "
        f"-X {release.MODULE}.Date={datetime.datetime.fromtimestamp(epoch, datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')}"
    )
    public_key = env.get("LINKPORT_UPDATE_PUBLIC_KEY", "")
    if public_key:
        import base64
        if len(base64.b64decode(public_key, validate=True)) != 32:
            raise ValueError("LINKPORT_UPDATE_PUBLIC_KEY must contain a base64 Ed25519 public key")
        flags += f" -X main.updatePublicKey={public_key}"
    update_base = env.get("LINKPORT_UPDATE_BASE_URL", "")
    if update_base:
        parsed = urlparse(update_base)
        if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.path not in ("", "/") or any(c.isspace() for c in update_base):
            raise ValueError("LINKPORT_UPDATE_BASE_URL must be a public HTTPS origin")
        flags += f" -X main.updateBaseURL={update_base.rstrip('/')}"
    if system == "darwin":
        flags += " -extldflags=-mmacosx-version-min=14.5"
    elif system == "windows":
        flags += " -H windowsgui"
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="linkport-desktop-") as temporary:
        staging = Path(temporary)
        binary = staging / ("Linkport.exe" if system == "windows" else "linkport-desktop")
        build_executables(go, env, flags, binary)
        if system == "darwin":
            bundle = mac_bundle(staging, binary, base, env)
            destination = output / "Linkport.app"
            if destination.exists():
                saved = plistlib.loads((destination / "Contents/Info.plist").read_bytes())
                if saved.get("CFBundleIdentifier") != IDENTIFIER:
                    raise ValueError("refusing to replace an application not owned by Linkport")
                shutil.rmtree(destination)
            shutil.copytree(bundle, destination)
            entries = [
                (str(item.relative_to(staging)), item.read_bytes(), item.stat().st_mode & 0o777)
                for item in sorted(bundle.rglob("*")) if item.is_file()
            ]
        else:
            shutil.copy2(binary, output / binary.name)
            entries = [(binary.name, binary.read_bytes(), 0o755)]
        # Wails replaces one application payload per update. Extra top-level
        # documentation or metadata would make its archive extractor reject it.
        archive = output / f"linkport-desktop_{version}_{system}_{arch}.zip"
        release.write_archive(archive, entries, epoch)
    write_checksums(output)
    print(f"{archive}\ncommit={commit}")


def write_checksums(output):
    lines = []
    for archive in sorted(output.glob("*.zip")):
        if archive.is_file():
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            lines.append(f"{digest}  {archive.name}\n")
    (output / "checksums.txt").write_text("".join(lines), encoding="utf-8", newline="\n")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=ROOT / "dist/desktop")
    parser.add_argument("--target", choices=["_".join(target) for target in release.TARGETS])
    parser.add_argument("--go", default="go")
    parser.add_argument("--tag")
    arguments = parser.parse_args()
    build(arguments.output.resolve(), arguments.target, arguments.go, arguments.tag)


if __name__ == "__main__":
    main()
