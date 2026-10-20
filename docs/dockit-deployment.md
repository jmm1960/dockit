# Configuration and deployment

This document describes the Dockit executable. The inherited Watchtower documents
in this directory describe the upstream application and are not a complete reference
for Dockit's configuration interface.

## Images and installation

Published images are available at `ghcr.io/jmm1960/dockit` for `linux/amd64` and
`linux/arm64`:

| Tag | Purpose |
| --- | --- |
| `latest` | Latest successful publication from the project workflow. |
| `sha-<full-commit-id>` | Image associated with a specific source revision. |

The package is public and supports anonymous pulls. To install using the example
Compose file in the repository:

```sh
docker compose pull dockit
docker compose up -d dockit
docker compose logs --tail 50 dockit
```

Keep the Bark key in a local `.env` file:

```dotenv
BARK_KEY=your-device-key
```

For a fixed deployment, replace `latest` with a commit tag or image digest. Update
the Dockit service explicitly when upgrading the maintenance tool itself.

## Global settings

| Environment variable | Default | Description |
| --- | --- | --- |
| `TZ` | `UTC` | IANA time zone used by all cron schedules. |
| `DOCKIT_UPDATE_CRON` | `0 4 * * *` | Default image update check schedule. |
| `DOCKIT_POLL_INTERVAL` | `5s` | Container discovery and health observation interval; at least one second. |
| `DOCKIT_STOP_TIMEOUT` | `10s` | Grace period for stopping a container; at least one second. |
| `DOCKIT_BARK_SERVER` | `https://api.day.app` | HTTPS Bark server URL; a path prefix is supported. |
| `DOCKIT_BARK_KEY` | Empty | Device key. An empty value disables notifications. |
| `DOCKIT_BARK_GROUP` | `Dockit` | Notification group. |

Duration values use Go duration syntax, for example `30s`, `10m`, or `1h`.

The Docker client also reads `DOCKER_HOST`, `DOCKER_API_VERSION`,
`DOCKER_TLS_VERIFY`, and `DOCKER_CERT_PATH`. The default connection is the local
Unix socket at `/var/run/docker.sock`.

### Docker Engine compatibility

The inherited Docker v24 client negotiates up to API 1.43. Docker Engine 29 requires
API 1.44 or later. For Engine 29, set:

```yaml
environment:
  DOCKER_API_VERSION: "1.44"
```

Container discovery, restarts, image pulls, and container replacement have been
verified against Engine 29.2.1 with this setting. Set an appropriate version for
other engines; specifying a version newer than the daemon supports will fail.

## Container labels

| Label | Default | Description |
| --- | --- | --- |
| `dockit.enable` | `false` | Opt the container into management. |
| `dockit.update.enable` | `false` | Enable image checks, pulls, and container replacement. |
| `dockit.update.cron` | Global update schedule | Override the image check schedule for this container. |
| `dockit.restart.cron` | Unset | Schedule a restart of the existing container. |
| `dockit.heal.enable` | `false` | Enable recovery of containers Docker marks unhealthy. |
| `dockit.heal.unhealthy-for` | `0s` | Required duration of observed unhealthy status before attempting recovery. |
| `dockit.heal.cooldown` | `10m` | Minimum interval between recovery attempts. |
| `dockit.heal.max-restarts` | `3` | Maximum recovery attempts within the sliding window. Failed attempts count. |
| `dockit.heal.window` | `1h` | Sliding window used to limit recovery attempts. |
| `dockit.self` | Unset | A value of `true` excludes the maintenance container itself. |

Labels belong to containers, not registry tags. Add them under the service's
`labels` section in Compose or pass them with `docker run --label`.

### Schedule inheritance

```yaml
# Use the global update schedule.
labels:
  dockit.enable: "true"
  dockit.update.enable: "true"
```

```yaml
# Check this service every day at 03:00 instead.
labels:
  dockit.enable: "true"
  dockit.update.enable: "true"
  dockit.update.cron: "0 3 * * *"
```

Schedules use five-field cron syntax. Six-field expressions and descriptors such
as `@daily` are not accepted. An invalid policy is logged and that container is
skipped. Polling and ongoing maintenance can delay execution beyond the exact cron
time. Missed occurrences are not replayed individually.

### Health recovery

Docker runs the service's configured `HEALTHCHECK`; Dockit observes the resulting
status. Without a health check, Dockit does not perform its own application probe.

```yaml
labels:
  dockit.enable: "true"
  dockit.heal.enable: "true"
  dockit.heal.unhealthy-for: "2m"
  dockit.heal.cooldown: "10m"
  dockit.heal.max-restarts: "3"
  dockit.heal.window: "1h"
```

At the restart limit, Dockit pauses further attempts and sends a notification.
Attempts can resume as earlier attempts expire from the sliding window. Counters
and observed health durations are held in memory and reset when Dockit restarts.

### Watchtower label compatibility

`com.centurylinklabs.watchtower.enable=true` enables management and updates when
the corresponding Dockit labels are absent. Explicit Dockit settings take precedence:
`dockit.enable=false` disables management, and `dockit.update.enable=false`
disables image updates.

The reused update engine also recognizes the upstream `monitor-only` and `no-pull`
labels. Review these labels during migration. Other Watchtower CLI flags and
`WATCHTOWER_*` environment variables are not Dockit's public configuration interface.

## Private registries and certificates

Mount a Docker `config.json` read-only and set `DOCKER_CONFIG` to its directory:

```yaml
environment:
  DOCKER_CONFIG: /run/registry
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
  - ./registry-config.json:/run/registry/config.json:ro
```

A configuration using a credential helper also requires that helper executable
to be available inside the container. The default scratch image does not include
external credential helpers. The upstream `REPO_USER` and `REPO_PASS` environment
variables are supported but apply globally; prefer credentials scoped by registry.
Socket access alone does not expose the host's Docker login configuration.

Registry digest requests verify TLS certificates. For an internal CA, supply a
trusted CA bundle to Dockit, for example through `SSL_CERT_FILE`. Configure the
Docker daemon's registry trust separately: its certificate store is independent
of the Dockit container's store.

## Notifications

Bark receives a JSON POST to `<server>/push`. Dockit sends update and restart
results, and a notification when health recovery reaches its attempt limit. An
unchanged image does not generate a notification.

Delivery is asynchronous, with a bounded queue of 64 messages, a ten-second request
timeout, and up to three attempts. Redirects are rejected. A retry can result in a
duplicate notification. Queued notifications are not persisted across shutdowns.

## Operational behavior

Maintenance operations are serialized. In the same discovery cycle, image updates
take priority over scheduled restarts, followed by health recovery. After an update,
Dockit rediscovers the container before considering further operations.

Only running containers are managed. Dockit does not intentionally start manually
stopped services. Use Docker restart policies such as `unless-stopped` to handle
process exits. Dockit excludes its own container from maintenance.

An image update pulls the configured reference, stops and removes the old container,
then creates and starts its replacement. Deployment configuration is reconstructed
using Watchtower's update engine. The workflow does not provide readiness validation,
automatic rollback, or recovery of an interrupted update. Old images and volumes are
not automatically cleaned up.

Named-volume contents and maintenance labels have been verified across replacement.
Validate anonymous volumes, shared network namespaces, and other specialized
configurations before enabling unattended updates. Dockit operates on individual
containers rather than orchestrating an entire Compose application's dependencies.

When replacing another updater, stop it before allowing Dockit to manage the same
containers. Changing service labels requires container recreation; plan that change
for an appropriate maintenance window.
