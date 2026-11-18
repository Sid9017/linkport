#!/usr/bin/env python3
"""Regression checks for updater-compatible desktop archives and their CLI."""
from contextlib import redirect_stdout
import io
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock
import zipfile

import desktop


class BundledCLITests(unittest.TestCase):
    def test_cli_is_console_mode_and_payload_is_removed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with mock.patch.object(desktop, "ROOT", root), mock.patch.object(desktop, "DESKTOP", root / "desktop"), mock.patch.object(desktop.subprocess, "run") as run:
                payload = root / "desktop/assets/cli/linkport.bin"
                def build(command, **kwargs):
                    if command[-1] == "./cmd/linkport":
                        payload.write_bytes(b"console executable")
                    else:
                        self.assertEqual(payload.read_bytes(), b"console executable")
                run.side_effect = build
                desktop.build_executables("go", {}, "-s -w -H windowsgui", root / "Linkport.exe")
                cli, gui = run.call_args_list
                self.assertNotIn("windowsgui", cli.args[0][cli.args[0].index("-ldflags") + 1])
                self.assertIn("windowsgui", gui.args[0][gui.args[0].index("-ldflags") + 1])
                self.assertIn("production,bundledcli", gui.args[0])
                self.assertEqual(cli.kwargs["cwd"], root)
                self.assertFalse(payload.exists())

    def test_failed_desktop_build_cleans_payload(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with mock.patch.object(desktop, "ROOT", root), mock.patch.object(desktop, "DESKTOP", root / "desktop"), mock.patch.object(desktop.subprocess, "run") as run:
                payload = root / "desktop/assets/cli/linkport.bin"
                def fail(command, **kwargs):
                    if command[-1] == "./cmd/linkport":
                        payload.write_bytes(b"payload")
                    else:
                        raise subprocess.CalledProcessError(1, command)
                run.side_effect = fail
                with self.assertRaises(subprocess.CalledProcessError):
                    desktop.build_executables("go", {}, "-s", root / "gui")
                self.assertFalse(payload.exists())


class DesktopArchiveTests(unittest.TestCase):
    def test_complete_native_set_rejects_zip_or_missing_target(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            for system, arch in desktop.release.TARGETS:
                payload = {"darwin": b"koly" + bytes(508), "windows": b"MZfixture", "linux": b"!<arch>\n"}[system]
                (output / desktop.archive_name("1.2.3", system, arch)).write_bytes(payload)
            desktop.validate_archives(output, "1.2.3")
            extra = output / "legacy.zip"
            extra.write_bytes(b"ZIP")
            with self.assertRaisesRegex(ValueError, "mismatch"):
                desktop.validate_archives(output, "1.2.3")
            extra.unlink()
            next(output.glob("*.deb")).unlink()
            with self.assertRaisesRegex(ValueError, "mismatch"):
                desktop.validate_archives(output, "1.2.3")

    def test_debian_metadata_and_desktop_launcher(self):
        for arch in ("amd64", "arm64"):
            with self.subTest(arch=arch), tempfile.TemporaryDirectory() as directory:
                output = Path(directory)
                binary = output / "linkport-desktop"
                binary.write_bytes(b"desktop fixture")
                def inspect(command, **kwargs):
                    if "--build" in command:
                        stage = Path(command[-2])
                        control = (stage / "DEBIAN/control").read_text()
                        self.assertIn(f"Architecture: {arch}\n", control)
                        self.assertIn("Version: 1.2.3~nightly.20261118000000.1\n", control)
                        self.assertIn("Package: linkport\n", control)
                        self.assertIn("libwebkitgtk-6.0-4", control)
                        self.assertEqual((stage / "usr/bin/linkport-desktop").read_bytes(), binary.read_bytes())
                        self.assertIn("Exec=linkport-desktop", (stage / "usr/share/applications/linkport.desktop").read_text())
                        self.assertTrue((stage / "usr/share/icons/hicolor/256x256/apps/linkport.png").is_file())
                with mock.patch.object(desktop.subprocess, "run", side_effect=inspect):
                    desktop.linux_package(binary, output / "fixture.deb", "1.2.3-nightly.20261118000000.1", arch, {})

    def test_each_target_contains_only_its_application_payload(self):
        for system, arch in desktop.release.TARGETS:
            with self.subTest(system=system, arch=arch), tempfile.TemporaryDirectory() as directory:
                output = Path(directory)
                executable = "Linkport.exe" if system == "windows" else "linkport-desktop"

                def build_binary(go, env, flags, binary):
                    binary.write_bytes(b"desktop with embedded CLI")
                    binary.chmod(0o755)

                def bundle_binary(staging, binary, version, env):
                    bundle = staging / "Linkport.app"
                    destination = bundle / "Contents/MacOS" / binary.name
                    destination.parent.mkdir(parents=True)
                    destination.write_bytes(binary.read_bytes())
                    destination.chmod(0o755)
                    (bundle / "Contents/Info.plist").write_bytes(b"bundle metadata")
                    return bundle

                def dmg(bundle, destination, env):
                    self.assertEqual((bundle / "Contents/MacOS/linkport-desktop").read_bytes(), b"desktop with embedded CLI")
                    destination.write_bytes(b"koly" + bytes(508))

                def deb(binary, destination, version, arch, env):
                    self.assertEqual(binary.read_bytes(), b"desktop with embedded CLI")
                    destination.write_bytes(b"!<arch>\n")

                with mock.patch.object(desktop.release, "read_version", return_value="1.2.3"), \
                     mock.patch.object(desktop.release, "build_version", return_value=("1.2.3", "stable")), \
                     mock.patch.object(desktop.release, "source_epoch", return_value=1700000000), \
                     mock.patch.object(desktop.release, "run", side_effect=["fixture-commit", ""]), \
                     mock.patch.object(desktop, "go_output", return_value=f"{system}\n{arch}"), \
                     mock.patch.object(desktop, "build_executables", side_effect=build_binary), \
                     mock.patch.object(desktop, "mac_bundle", side_effect=bundle_binary), \
                     mock.patch.object(desktop, "mac_disk_image", side_effect=dmg), \
                     mock.patch.object(desktop, "linux_package", side_effect=deb), \
                     redirect_stdout(io.StringIO()):
                    desktop.build(output, f"{system}_{arch}", "go", None)

                package = output / desktop.archive_name("1.2.3", system, arch)
                self.assertTrue(package.is_file())
                self.assertFalse(list(output.glob("*.zip")))
                if system == "windows":
                    self.assertEqual(package.read_bytes(), b"desktop with embedded CLI")
                self.assertIn(package.name, (output / "checksums.txt").read_text())


if __name__ == "__main__":
    unittest.main()
