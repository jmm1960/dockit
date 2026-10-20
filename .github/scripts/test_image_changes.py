import contextlib
import io
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import image_changes


class ChangeDetectionTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.previous = os.getcwd()
        os.chdir(self.directory.name)
        self.git('init', '-q')
        self.git('config', 'user.name', 'CI Test')
        self.git('config', 'user.email', 'ci@example.invalid')
        Path('main.go').write_text('package main\n')
        self.base = self.commit()

    def tearDown(self):
        os.chdir(self.previous)
        self.directory.cleanup()

    def git(self, *args):
        return subprocess.check_output(['git', *args], text=True).strip()

    def commit(self):
        self.git('add', '-A')
        self.git('commit', '-qm', 'fixture')
        return self.git('rev-parse', 'HEAD')

    def requires_image(self, before, after):
        paths = image_changes.changed_paths('push', {'before': before}, after)
        return any(image_changes.affects_image(p) for p in paths)

    def test_docs_and_tests_do_not_build(self):
        Path('README.md').write_text('Documentation\n')
        Path('main_test.go').write_text('package main\n')
        Path('Makefile').write_text('test:\n\tgo test ./...\n')
        self.assertFalse(self.requires_image(self.base, self.commit()))

    def test_entire_push_is_checked(self):
        Path('main.go').write_text('package main\nfunc main() {}\n')
        runtime = self.commit()
        Path('README.md').write_text('Documentation\n')
        head = self.commit()
        self.assertFalse(self.requires_image(runtime, head))
        self.assertTrue(self.requires_image(self.base, head))

    def test_runtime_deletion_is_checked(self):
        Path('main.go').unlink()
        Path('README.md').write_text('Documentation\n')
        self.assertTrue(self.requires_image(self.base, self.commit()))

    def test_rename_out_of_runtime_is_checked(self):
        Path('main.go').rename('main_test.go')
        self.assertTrue(self.requires_image(self.base, self.commit()))

    def test_dependencies_and_build_inputs_trigger_build(self):
        for path in ['go.mod', 'go.sum', 'Dockerfile', '.dockerignore',
                     '.github/workflows/publish.yml', '.github/scripts/image_changes.py']:
            with self.subTest(path=path):
                self.assertTrue(image_changes.affects_image(path))

    def test_manual_runs_and_tags_force_build(self):
        for name, ref in [('workflow_dispatch', 'refs/heads/main'), ('push', 'refs/tags/v1.0.0')]:
            with self.subTest(event=name), patch.dict(os.environ, {'GITHUB_EVENT_NAME': name, 'GITHUB_REF': ref}):
                result = io.StringIO()
                with contextlib.redirect_stdout(result):
                    image_changes.main()
                self.assertEqual(result.getvalue().strip(), 'true')


if __name__ == '__main__':
    unittest.main()
