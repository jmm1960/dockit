# Local integration testing

The integration suite runs Dockit against a real local Docker Engine. It creates
disposable services and a temporary registry, exercises maintenance operations,
and removes its test containers, images, and data volume when it finishes.

## Requirements

- Python 3 and the Docker CLI, including Docker Buildx.
- A running local Docker Engine, Docker Desktop, or OrbStack.
- Access to the daemon through a local Unix-socket Docker context.
- Network access to pull build dependencies, Alpine, and the registry image.

The suite refuses to run while an existing maintenance service or an opted-in
container is running. It uses the selected Docker context and mounts the daemon
socket into its temporary Dockit container. The default daemon-side socket path is
`/var/run/docker.sock`; override it with `DOCKIT_TEST_SOCKET` when needed.

## Run from source

```sh
make integration
```

Or invoke the script directly:

```sh
python3 scripts/integration.py
```

The script builds `dockit:local` from the root Dockerfile. The Go toolchain runs
inside the build container; a host Go installation is not required for this suite.
The resulting image is retained for subsequent local development.

## Test a published image

```sh
docker pull ghcr.io/jmm1960/dockit:latest
python3 scripts/integration.py --image ghcr.io/jmm1960/dockit:latest
```

Use a `sha-<full-commit-id>` tag instead of `latest` to test a fixed publication.
The `--image` option skips the source build. Increase the maintenance observation
timeout if required:

```sh
python3 scripts/integration.py --timeout 240
```

## Checks

| Check | Expected result |
| --- | --- |
| Source image | The image starts and reports its version. |
| Health recovery | Removing a health marker causes an existing container to restart. |
| Scheduled restart | A per-minute cron label restarts the existing container. |
| Registry update | A new image under the same tag replaces the old container. |
| Configuration and storage | The replacement retains its maintenance labels and named-volume contents. |
| Local image policy | A container with updates disabled remains unchanged, even with an update cron label. |
| Missing health check | A container without HEALTHCHECK is not restarted by health recovery. |
| Manual stop | An opted-in container stopped before discovery remains stopped. |

Cron-based checks need to cross a minute boundary. Allow several minutes for a
cold build and the full test run. Bark is disabled in the integration service;
notification payloads and HTTPS behavior are covered by the Go unit tests.

## Resource lifecycle

Every test container, fixture image, and volume uses a unique `dockit-it-` prefix.
The temporary registry listens only on a randomly allocated loopback port. Cleanup
stops the supervisor first, then removes its fixtures and data. Unrelated images,
containers, and volumes are not pruned. Downloaded base images and Docker build
cache remain available for reuse.

If the test process is forcibly terminated before cleanup can run, inspect resources
with the `io.dockit.integration` label and remove only those belonging to that run.

## Unit tests

With Go installed:

```sh
make test
go test -race ./internal/dockit ./pkg/registry/digest
```
