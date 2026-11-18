#!/usr/bin/env python3
"""Regression checks for the CLI carried by desktop archives."""
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

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


if __name__ == "__main__":
    unittest.main()
