#!/usr/bin/env python3
"""Remove only untagged GHCR manifests unreachable from every tagged version."""
import datetime
import hashlib
import json
import os
import urllib.request


ACCEPT = ', '.join((
    'application/vnd.oci.image.index.v1+json',
    'application/vnd.oci.image.manifest.v1+json',
    'application/vnd.docker.distribution.manifest.list.v2+json',
    'application/vnd.docker.distribution.manifest.v2+json',
))


def request(url, headers, method='GET'):
    with urllib.request.urlopen(urllib.request.Request(
            url, headers=headers, method=method), timeout=60) as response:
        data = response.read()
        return json.loads(data) if data else None


def tagged(versions):
    return {v['name']: tuple(sorted(v['metadata']['container']['tags']))
            for v in versions if v['metadata']['container']['tags']}


def reachable(roots, fetch):
    protected = set()
    pending = list(roots)
    while pending:
        digest = pending.pop()
        if digest in protected:
            continue
        manifest = fetch(digest)  # Any missing manifest aborts before deleting.
        protected.add(digest)
        pending.extend(child['digest'] for child in manifest.get('manifests', []))
    return protected


def main():
    repository = os.environ['GITHUB_REPOSITORY'].lower()
    owner, package = repository.split('/')
    api = f'https://api.github.com/users/{owner}/packages/container/{package}/versions'
    headers = {'Authorization': 'Bearer ' + os.environ['GH_TOKEN'],
               'Accept': 'application/vnd.github+json',
               'X-GitHub-Api-Version': '2022-11-28'}

    def versions():
        result = []
        page = 1
        while True:
            batch = request(f'{api}?per_page=100&page={page}', headers)
            result.extend(batch)
            if len(batch) < 100:
                return result
            page += 1

    token = request('https://ghcr.io/token?service=ghcr.io&scope=repository:'
                    + repository + ':pull', {})['token']
    registry_headers = {'Authorization': 'Bearer ' + token, 'Accept': ACCEPT}

    def manifest(digest):
        url = f'https://ghcr.io/v2/{repository}/manifests/{digest}'
        with urllib.request.urlopen(urllib.request.Request(
                url, headers=registry_headers), timeout=60) as response:
            data = response.read()
        if 'sha256:' + hashlib.sha256(data).hexdigest() != digest:
            raise RuntimeError('Manifest digest mismatch: ' + digest)
        return json.loads(data)

    inventory = versions()
    roots = tagged(inventory)
    if not roots:
        raise RuntimeError('No tagged versions found; refusing cleanup')
    protected = reachable(roots, manifest)
    # Avoid new uploads that might belong to an in-progress publication.
    minimum_age = int(os.environ.get('MINIMUM_AGE_MINUTES', '60'))
    if minimum_age < 0:
        raise ValueError('Minimum age must not be negative')
    cutoff = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(minutes=minimum_age)
    candidates = [v for v in inventory if v['name'] not in protected
                  and not v['metadata']['container']['tags']
                  and datetime.datetime.fromisoformat(v['created_at'].replace('Z', '+00:00')) < cutoff]
    dry_run = os.environ.get('DRY_RUN', 'true').lower() != 'false'
    print(f'Tagged roots: {len(roots)}; protected manifests: {len(protected)}; '
          f'unused versions: {len(candidates)}; dry run: {dry_run}', flush=True)
    for v in inventory:
        print(('DELETE' if v in candidates else 'KEEP'), v['id'], v['name'],
              v['metadata']['container']['tags'], flush=True)
    deleted = 0
    if not dry_run:
        for v in candidates:
            current = versions()
            if tagged(current) != roots:
                raise RuntimeError('Tags changed during cleanup; aborting')
            fresh = next((item for item in current if item['id'] == v['id']), None)
            if fresh is None:
                continue
            if fresh['metadata']['container']['tags'] or fresh['name'] in protected:
                raise RuntimeError('Candidate became protected; aborting')
            request(f"{api}/{v['id']}", headers, 'DELETE')
            deleted += 1
            print('Deleted', v['id'], flush=True)
        reachable(roots, manifest)
    remaining = versions()
    summary = (f'Deleted {deleted} unused versions. Remaining: '
               f'{len(tagged(remaining))} tagged, '
               f'{len(remaining) - len(tagged(remaining))} untagged.\n')
    print(summary)
    if os.environ.get('GITHUB_STEP_SUMMARY'):
        with open(os.environ['GITHUB_STEP_SUMMARY'], 'a') as output:
            output.write(summary)


if __name__ == '__main__':
    main()
