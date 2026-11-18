#!/usr/bin/env python3
"""Release notes must retain the selected version's changes."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("release_notes", Path(__file__).with_name("release-notes.py"))
release_notes = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release_notes)


class ReleaseNotesTests(unittest.TestCase):
    def test_only_current_version_changes_are_included(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "CHANGELOG.md").write_text(
                "# Changelog\n\n## 0.0.6 — desktop update packages\n\n"
                "- Keep one application payload.\n- Inline publisher signatures.\n\n"
                "## 0.0.5 — previous version\n\n- Previous changes.\n",
                encoding="utf-8",
            )
            with mock.patch.object(release_notes.release, "ROOT", root), \
                 mock.patch.object(release_notes.release, "read_version", return_value="0.0.6"), \
                 mock.patch.object(release_notes.release, "build_version", return_value=("0.0.6", "stable")), \
                 mock.patch.object(release_notes.release, "run", return_value="fixture-commit"):
                notes = release_notes.notes("v0.0.6")
            self.assertIn("- Keep one application payload.", notes)
            self.assertIn("- Inline publisher signatures.", notes)
            self.assertNotIn("Previous changes", notes)


if __name__ == "__main__":
    unittest.main()
