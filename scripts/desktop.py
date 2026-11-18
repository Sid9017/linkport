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
FORMATS = {"darwin": "dmg", "windows": "exe", "linux": "deb"}

def archive_name(version, system, arch):
    extension = FORMATS[system]
    return f"linkport-desktop_{version}_{system}_{arch}.{extension}"


def mac_disk_image(bundle, destination, env):
    with tempfile.TemporaryDirectory(prefix="linkport-dmg-") as directory:
        contents = Path(directory)
        shutil.copytree(bundle, contents / "Linkport.app")
        (contents / "Applications").symlink_to("/Applications", target_is_directory=True)
        subprocess.run([
            "hdiutil", "create", "-quiet", "-volname", "Linkport",
            "-srcfolder", str(contents), "-fs", "HFS+", "-format", "UDZO", "-ov", str(destination),
        ], check=True, env=env)
    subprocess.run(["hdiutil", "verify", "-quiet", str(destination)], check=True, env=env)

def linux_package(binary, destination, version, arch, env):
    with tempfile.TemporaryDirectory(prefix="linkport-deb-") as directory:
        root = Path(directory)
        installed = root / "usr/bin/linkport-desktop"
        installed.parent.mkdir(parents=True)
        shutil.copy2(binary, installed)
        installed.chmod(0o755)
        control = root / "DEBIAN/control"
        control.parent.mkdir()
        control.write_text(
            "Package: linkport\n"
            f"Version: {version.replace('-', '~', 1)}\n"
            f"Architecture: {arch}\n"
            "Maintainer: Linkport contributors <Sid9017@users.noreply.github.com>\n"
            "Section: utils\nPriority: optional\n"
            "Depends: libc6, libgtk-4-1, libwebkitgtk-6.0-4, xdg-utils\n"
            "Homepage: https://linkport.grezl.link\n"
            "Description: Explicit serial sharing between paired devices\n"
            " Share selected remote serial ports with your local debugging tools.\n",
            encoding="utf-8",
        )
        launcher = root / "usr/share/applications/linkport.desktop"
        launcher.parent.mkdir(parents=True)
        launcher.write_text(
            "[Desktop Entry]\nType=Application\nName=Linkport\n"
            "Exec=linkport-desktop\nIcon=linkport\nTerminal=false\n"
            "Categories=Development;Utility;\n", encoding="utf-8",
        )
        icon = root / "usr/share/icons/hicolor/256x256/apps/linkport.png"
        icon.parent.mkdir(parents=True)
        shutil.copy2(DESKTOP / "assets/app.png", icon)
        subprocess.run(["dpkg-deb", "-Zgzip", "--build", "--root-owner-group", str(root), str(destination)],
                       check=True, env=env)
        subprocess.run(["dpkg-deb", "--info", str(destination)], check=True, env=env, stdout=subprocess.DEVNULL)


def validate_archives(directory, version):
    expected = {archive_name(version, system, arch) for system, arch in release.TARGETS}
    actual = {item.name for item in directory.iterdir() if item.suffix in (".zip", ".dmg", ".exe", ".deb")}
    if actual != expected:
        raise ValueError(f"desktop archive mismatch: {actual ^ expected}")
    for system, arch in release.TARGETS:
        artifact = directory / archive_name(version, system, arch)
        if system == "darwin":
            with artifact.open("rb") as stream:
                stream.seek(-512, 2)
                if stream.read(4) != b"koly":
                    raise ValueError(f"invalid UDIF disk image: {artifact.name}")
            continue
        with artifact.open("rb") as stream:
            signature = stream.read(8)
        if system == "windows" and signature[:2] != b"MZ":
            raise ValueError(f"invalid Windows executable: {artifact.name}")
        if system == "linux" and signature != b"!<arch>\n":
            raise ValueError(f"invalid Debian package: {artifact.name}")


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
        archive = output / archive_name(version, system, arch)
        if system == "darwin":
            bundle = mac_bundle(staging, binary, base, env)
            destination = output / "Linkport.app"
            if destination.exists():
                saved = plistlib.loads((destination / "Contents/Info.plist").read_bytes())
                if saved.get("CFBundleIdentifier") != IDENTIFIER:
                    raise ValueError("refusing to replace an application not owned by Linkport")
                shutil.rmtree(destination)
            shutil.copytree(bundle, destination)
            mac_disk_image(bundle, archive, env)
        elif system == "windows":
            shutil.copy2(binary, archive)
        else:
            linux_package(binary, archive, version, arch, env)
    write_checksums(output)
    print(f"{archive}\ncommit={commit}")


def write_checksums(output):
    lines = []
    for archive in sorted(item for item in output.iterdir() if item.name.startswith("linkport-desktop_") and item.suffix in (".dmg", ".exe", ".deb")):
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
