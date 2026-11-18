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

                with mock.patch.object(desktop.release, "read_version", return_value="1.2.3"), \
                     mock.patch.object(desktop.release, "build_version", return_value=("1.2.3", "stable")), \
                     mock.patch.object(desktop.release, "source_epoch", return_value=1700000000), \
                     mock.patch.object(desktop.release, "run", side_effect=["fixture-commit", ""]), \
                     mock.patch.object(desktop, "go_output", return_value=f"{system}\n{arch}"), \
                     mock.patch.object(desktop, "build_executables", side_effect=build_binary), \
                     mock.patch.object(desktop, "mac_bundle", side_effect=bundle_binary), \
                     redirect_stdout(io.StringIO()):
                    desktop.build(output, f"{system}_{arch}", "go", None)

                archive = output / f"linkport-desktop_1.2.3_{system}_{arch}.zip"
                with zipfile.ZipFile(archive) as packaged:
                    payload = "Linkport.app" if system == "darwin" else executable
                    self.assertEqual({name.split("/")[0] for name in packaged.namelist()}, {payload})
                    entry = f"{payload}/Contents/MacOS/{executable}" if system == "darwin" else payload
                    self.assertEqual(packaged.read(entry), b"desktop with embedded CLI")
                    # A simulated .app uses the host filesystem's permissions;
                    # Windows cannot model Unix executable bits. Native Mac
                    # packaging must preserve the source bundle's actual mode.
                    mode = (output / entry).stat().st_mode & 0o777 if system == "darwin" else 0o755
                    self.assertEqual(packaged.getinfo(entry).external_attr >> 16, 0o100000 | mode)
                    if system == "darwin":
                        self.assertEqual(packaged.read(f"{payload}/Contents/Info.plist"), b"bundle metadata")
                    else:
                        self.assertEqual(packaged.namelist(), [payload])
                self.assertIn(archive.name, (output / "checksums.txt").read_text())


if __name__ == "__main__":
    unittest.main()
