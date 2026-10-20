# Dockit

[![Build](https://github.com/jmm1960/dockit/actions/workflows/publish.yml/badge.svg)](https://github.com/jmm1960/dockit/actions/workflows/publish.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE.md)

Docker container maintenance, configured through labels.

Dockit checks registries for image updates, replaces containers with the new image,
restarts containers on a schedule, and restarts unhealthy containers. Notifications
are delivered through [Bark](https://github.com/Finb/Bark).

Derived from [Watchtower](https://github.com/containrrr/watchtower), Dockit retains
its image update engine and adds per-container scheduling and health recovery.
It runs as a single service with no web interface or management HTTP listener.

## Features

- **Automatic updates:** track changes to a container's image tag, pull the new image,
  and recreate the container with its deployment configuration.
- **Per-container schedules:** inherit a global update schedule or override it with
  a container label.
- **Scheduled restarts:** restart an existing container using a separate cron schedule.
- **Health recovery:** restart containers marked `unhealthy` by Docker, with a cooldown
  and a limit on restart attempts.
- **Bark notifications:** report maintenance results through the public Bark service
  or a self-hosted HTTPS endpoint.

## Quick start

The image is available from **`ghcr.io/jmm1960/dockit:latest`** for `linux/amd64`
and `linux/arm64`. Public images can be pulled without signing in.

Create a `compose.yaml`:

```yaml
services:
  dockit:
    image: ghcr.io/jmm1960/dockit:latest
    restart: unless-stopped
    environment:
      TZ: UTC
      DOCKIT_UPDATE_CRON: "0 4 * * *"
      DOCKIT_BARK_KEY: ${BARK_KEY:-}
      # Required for Docker Engine 29 with the current Docker client:
      # DOCKER_API_VERSION: "1.44"
    labels:
      dockit.self: "true"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock

  redis:
    image: redis:7-alpine
    restart: unless-stopped
    volumes:
      - redis-data:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 30s
      timeout: 5s
      retries: 3
    labels:
      dockit.enable: "true"
      dockit.update.enable: "true"
      dockit.heal.enable: "true"
      dockit.heal.unhealthy-for: "2m"

volumes:
  redis-data:
```

Optionally set `BARK_KEY` in a local `.env` file, then start the services:

```sh
docker compose pull
docker compose up -d
```

Redis inherits the daily 04:00 update check. Its existing health check also enables
Dockit to restart it after it has remained unhealthy for two minutes. Without a
Bark key, maintenance still runs and notifications are disabled.

Docker socket access grants control over the Docker daemon. Run Dockit only on
hosts where it is trusted to manage containers.

## Configure a container

Both Docker Compose and `docker run` use the same container labels. Management is
opt-in: set `dockit.enable=true`, then enable the features required by that service.

```yaml
labels:
  dockit.enable: "true"
  dockit.update.enable: "true"
  dockit.update.cron: "0 3 * * *"  # Override the global update schedule.
  dockit.restart.cron: "0 5 * * 0" # Restart every Sunday at 05:00.
```

With the Docker CLI:

```sh
docker run -d --name redis --restart unless-stopped \
  --label dockit.enable=true \
  --label dockit.update.enable=true \
  redis:7-alpine
```

All cron expressions have five fields: minute, hour, day of month, month, and day
of week. They use Dockit's `TZ` setting. Checks begin at the next scheduled time;
starting Dockit does not immediately update containers. A cron label alone does
not enable image updates.

Docker container labels are set at creation time. Recreate a service after changing
its labels; restarting the existing container does not apply new labels. Dockit
discovers running containers every five seconds by default.

See the [configuration and deployment reference](docs/dockit-deployment.md) for
all labels, environment variables, private registry authentication, and scheduling
behavior.

## Runtime requirements

- Access to a Docker Engine through its Unix socket or a configured Docker endpoint.
- Network access to the image registries and, when enabled, the Bark server.
- A Docker `HEALTHCHECK` for any service that uses health recovery.

The current Docker client negotiates up to API 1.43 by default. **Docker Engine 29
requires `DOCKER_API_VERSION=1.44`**, which has been verified with Engine 29.2.1.
Older engines should use their supported API version or automatic negotiation.

Updates track the configured tag, such as `redis:7-alpine`; they do not select a
new major-version tag. Locally built images should have automatic updates disabled.
Existing Docker restart policies remain responsible for restarting exited processes.

Updates stop and replace containers, so service interruption is expected. A successful
notification means Docker accepted the start or restart operation; it does not certify
application readiness. See the reference for update and storage limitations.

## Build and test

```sh
go test ./...
go test -race ./internal/dockit ./pkg/registry/digest
go build -o /tmp/dockit .
```

The upstream color-format test assumes `NO_COLOR` is unset. In environments that
set it globally, use `env -u NO_COLOR go test ./...`.

To build a local image:

```sh
docker build -t dockit:local .
```

For a complete local Docker integration test:

```sh
make integration
```

This builds the source image and verifies image replacement, named-volume and label
preservation, scheduled restarts, and health recovery using disposable containers.
It also checks that update-disabled and manually stopped containers are left alone.
Test resources are removed on completion; `dockit:local` remains available for reuse.
See [local integration testing](docs/local-testing.md) for requirements and options.

GitHub Actions runs the test suite and publishes multi-platform images on pushes to
`main`. Each publication includes `latest` and `sha-<full-commit-id>` tags. Use a
commit tag or image digest when a fixed deployment version is required.

## Acknowledgments and license

Dockit builds on the work of the Watchtower maintainers and
[contributors](https://github.com/containrrr/watchtower/graphs/contributors).
The upstream source, documentation, and commit history are retained; see
[UPSTREAM.md](UPSTREAM.md) for provenance and compatibility notes.

Licensed under the [Apache License 2.0](LICENSE.md).
