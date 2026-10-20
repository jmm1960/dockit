#!/usr/bin/env python3
"""Run disposable Docker integration fixtures against the selected local daemon."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]
LABEL = 'io.dockit.integration'


def run(*args, capture=True, **kwargs):
    result = subprocess.run(args, check=True, text=True,
                            stdout=subprocess.PIPE if capture else None, **kwargs)
    return result.stdout.strip() if capture else ''


def inspect(name):
    result = subprocess.run(['docker', 'container', 'inspect', name],
                            capture_output=True, text=True)
    return json.loads(result.stdout)[0] if result.returncode == 0 else None


def preflight():
    endpoint = run('docker', 'context', 'inspect', '--format', '{{.Endpoints.docker.Host}}')
    if not os.environ.get('DOCKER_CONTEXT'):
        endpoint = os.environ.get('DOCKER_HOST') or endpoint
    if not endpoint.startswith('unix://'):
        raise RuntimeError('Select a local Unix-socket Docker context before running integration tests')
    run('docker', 'info', '--format', '{{.ServerVersion}}')
    ids = run('docker', 'ps', '-q').split()
    for cid in ids:
        container = inspect(cid)
        labels = container['Config'].get('Labels') or {}
        enabled = any(str(labels.get(k, '')).lower() in ('true', '1', 't', 'yes') for k in
                      ('dockit.enable', 'com.centurylinklabs.watchtower.enable', 'dockit.self'))
        if enabled or any(updater in container['Config']['Image'].lower() for updater in ('watchtower', 'autoheal')):
            raise RuntimeError('Stop existing maintenance services and opted-in containers before testing: '
                               + container['Name'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', help='Use an existing Dockit image instead of building dockit:local')
    parser.add_argument('--timeout', type=int, default=180, help='Maintenance observation timeout in seconds')
    args = parser.parse_args()
    preflight()
    image = args.image or 'dockit:local'
    if args.image is None:
        run('docker', 'build', '-t', image, str(ROOT), capture=False)
    run('docker', 'run', '--rm', image, '--version', capture=False)

    prefix = 'dockit-it-' + uuid.uuid4().hex[:10]
    names = {k: prefix + '-' + k for k in ('daemon', 'registry', 'heal', 'schedule', 'update', 'local', 'stopped')}
    volume = prefix + '-data'
    fixture_v1, fixture_v2 = prefix + ':v1', prefix + ':v2'
    created, image_refs = [], []
    volume_created = False

    def start(kind, *options):
        name = names[kind]
        run('docker', 'run', '-d', '--name', name, '--label', LABEL + '=' + prefix, *options)
        created.append(name)
        return name

    def fixture(kind, image_ref, labels, *options):
        label_args = []
        for key, value in labels.items():
            label_args.extend(['--label', key + '=' + value])
        return start(kind, '--memory', '64m', '--cpus', '0.5', *label_args, *options, image_ref)

    def build_fixture(tag, revision):
        dockerfile = ('FROM alpine:3.23\nARG REVISION\n'
                      'RUN echo "$REVISION" > /revision\n'
                      'CMD ["/bin/sh", "-c", "touch /tmp/dockit-healthy; '
                      "trap 'exit 0' TERM INT; while :; do sleep 1; done\"]\n")
        run('docker', 'build', '-t', tag, '--build-arg', 'REVISION=' + revision,
            '-', input=dockerfile, capture=False)
        image_refs.append(tag)

    try:
        build_fixture(fixture_v1, 'v1')
        start('registry', '-p', '127.0.0.1::5000', 'registry:2')
        port = inspect(names['registry'])['NetworkSettings']['Ports']['5000/tcp'][0]['HostPort']
        registry = '127.0.0.1:' + port
        deadline = time.monotonic() + 30
        while True:
            try:
                with urllib.request.urlopen('http://' + registry + '/v2/', timeout=2) as response:
                    if response.status == 200:
                        break
            except (OSError, urllib.error.URLError):
                if time.monotonic() > deadline:
                    raise RuntimeError('Temporary registry did not become reachable')
                time.sleep(1)
        remote_ref = registry + '/fixture/app:latest'
        run('docker', 'tag', fixture_v1, remote_ref)
        image_refs.append(remote_ref)
        run('docker', 'push', remote_ref, capture=False)
        run('docker', 'volume', 'create', '--label', LABEL + '=' + prefix, volume)
        volume_created = True
        enabled = {'dockit.enable': 'true', 'dockit.update.enable': 'false'}
        fixture('heal', fixture_v1, {**enabled, 'dockit.heal.enable': 'true',
                'dockit.heal.cooldown': '2s'}, '--health-cmd', 'test -f /tmp/dockit-healthy',
                '--health-interval', '1s', '--health-timeout', '1s', '--health-retries', '1')
        fixture('schedule', fixture_v1, {**enabled, 'dockit.restart.cron': '* * * * *'})
        fixture('local', fixture_v1, {**enabled, 'dockit.update.cron': '* * * * *',
                'dockit.heal.enable': 'true'})
        fixture('stopped', fixture_v1, {**enabled, 'dockit.restart.cron': '* * * * *'})
        run('docker', 'stop', '--timeout', '3', names['stopped'])
        update_labels = {'dockit.enable': 'true', 'dockit.update.enable': 'true',
                         'dockit.update.cron': '* * * * *'}
        fixture('update', remote_ref, update_labels,
                '--mount', 'type=volume,source=' + volume + ',target=/data')
        run('docker', 'exec', names['update'], '/bin/sh', '-c', 'echo preserved > /data/marker')
        initial = {kind: inspect(names[kind]) for kind in ('heal', 'schedule', 'update', 'local', 'stopped')}
        start('daemon', '-e', 'TZ=UTC', '-e', 'DOCKER_API_VERSION=1.44',
              '-e', 'DOCKIT_POLL_INTERVAL=1s', '-e', 'DOCKIT_STOP_TIMEOUT=3s',
              '-v', os.environ.get('DOCKIT_TEST_SOCKET', '/var/run/docker.sock') + ':/var/run/docker.sock', image)
        time.sleep(3)
        run('docker', 'exec', names['heal'], 'rm', '/tmp/dockit-healthy')
        build_fixture(fixture_v2, 'v2')
        run('docker', 'tag', fixture_v2, remote_ref)
        run('docker', 'push', remote_ref, capture=False)
        results = {}
        print('Waiting for health recovery, scheduled restart, and registry update...', flush=True)
        deadline = time.monotonic() + args.timeout
        while time.monotonic() < deadline and len(results) < 3:
            for kind in ('heal', 'schedule', 'update'):
                if kind in results:
                    continue
                current = inspect(names[kind])
                if current is None or not current['State']['Running']:
                    continue
                if kind == 'update':
                    if current['Id'] == initial[kind]['Id']:
                        continue
                    assert current['Image'] != initial[kind]['Image'], 'Image did not change'
                    assert run('docker', 'exec', names[kind], 'cat', '/revision') == 'v2'
                    assert run('docker', 'exec', names[kind], 'cat', '/data/marker') == 'preserved'
                    assert all(current['Config']['Labels'].get(k) == v for k, v in update_labels.items())
                    results[kind] = 'image replaced; named volume and labels preserved'
                elif current['State']['StartedAt'] != initial[kind]['State']['StartedAt']:
                    assert current['Id'] == initial[kind]['Id'], 'Restart unexpectedly replaced the container'
                    results[kind] = 'existing container restarted'
                if kind in results:
                    print('PASS ' + kind + ': ' + results[kind], flush=True)
            time.sleep(1)
        assert len(results) == 3, 'Timed out waiting for: ' + ', '.join(set(('heal', 'schedule', 'update')) - results.keys())
        local, stopped = inspect(names['local']), inspect(names['stopped'])
        assert local['Id'] == initial['local']['Id'] and local['State']['StartedAt'] == initial['local']['State']['StartedAt']
        assert not stopped['State']['Running'] and stopped['Id'] == initial['stopped']['Id']
        print('PASS local: update-disabled image and service without HEALTHCHECK left untouched', flush=True)
        print('PASS stopped: manually stopped container left stopped', flush=True)
        print('All local Docker integration checks passed.', flush=True)
    except BaseException:
        if names['daemon'] in created:
            subprocess.run(['docker', 'logs', '--tail', '100', names['daemon']])
        raise
    finally:
        # Stop the supervisor before removing the fixtures it manages.
        for name in [names['daemon']] + list(reversed(created)):
            if name not in created:
                continue
            subprocess.run(['docker', 'rm', '-f', '-v', name], capture_output=True)
            created.remove(name)
        if volume_created:
            subprocess.run(['docker', 'volume', 'rm', volume], check=True, capture_output=True)
        for ref in reversed(image_refs):
            subprocess.run(['docker', 'image', 'rm', ref], capture_output=True)
        print('Temporary test containers, registry, fixture images, and data volume removed.', flush=True)


if __name__ == '__main__':
    main()
