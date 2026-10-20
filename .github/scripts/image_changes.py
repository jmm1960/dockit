#!/usr/bin/env python3
"""Decide whether an Actions event requires rebuilding the runtime image."""
import json
import os
import subprocess

BUILD_INPUTS = {
    'Dockerfile', '.dockerignore', 'go.mod', 'go.sum',
    '.github/workflows/publish.yml', '.github/scripts/image_changes.py',
}


def affects_image(path):
    if path in BUILD_INPUTS:
        return True
    return (path.endswith('.go') and not path.endswith('_test.go')
            and '/mocks/' not in path and not path.startswith(('docs/', 'scripts/')))


def changed_paths(event_name, event, head):
    if event_name == 'pull_request':
        revisions = [event['pull_request']['base']['sha'] + '...' + event['pull_request']['head']['sha']]
    else:
        before = event.get('before', '')
        if not before or set(before) == {'0'}:
            return subprocess.check_output(['git', 'ls-tree', '-r', '--name-only', '-z', head]).decode().split('\0')
        if subprocess.run(['git', 'cat-file', '-e', before + '^{commit}'],
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
            subprocess.run(['git', 'fetch', '--no-tags', 'origin', before], check=True,
                           stdout=subprocess.DEVNULL)
        revisions = [before, head]
    # Include every commit in a push, as well as deletions and renames.
    return subprocess.check_output(['git', 'diff', '--no-renames', '--name-only', '-z', *revisions]).decode().split('\0')


def main():
    name = os.environ['GITHUB_EVENT_NAME']
    if name == 'workflow_dispatch' or os.environ.get('GITHUB_REF', '').startswith('refs/tags/'):
        print('true')
        return
    with open(os.environ['GITHUB_EVENT_PATH']) as source:
        event = json.load(source)
    print(str(any(affects_image(p) for p in changed_paths(name, event, os.environ['GITHUB_SHA']))).lower())


if __name__ == '__main__':
    main()
