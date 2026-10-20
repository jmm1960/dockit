# Source provenance

Dockit is a fork of [containrrr/watchtower](https://github.com/containrrr/watchtower),
based on commit `ca0e86e824ec05389ab972ea97d04d4bf0476e90` from the upstream
`main` branch. The upstream commit history and Apache-2.0 license are retained.

The Docker image inspection, registry authentication, digest comparison, and
container replacement code originate from Watchtower. Dockit's entry point uses
`internal/dockit` for label policies, scheduling, health recovery, and Bark delivery.
The original `cmd` package remains in the source tree but is not invoked by the
Dockit executable.

The module path remains `github.com/containrrr/watchtower` to preserve internal
imports and simplify comparison with upstream. Existing upstream documentation
is retained as a reference; Dockit configuration is documented in README.md and
`docs/dockit-deployment.md`.

The project workflow builds from the checked-out source and publishes Dockit images
to GHCR. The root Dockerfile is the supported build entry point. Legacy Dockerfiles
under `dockerfiles/` describe upstream builds; some fetch Watchtower from GitHub and
should not be used to build Dockit. The upstream precompiled `oryxBuildBinary` is
excluded from the current source tree and build context.

Thanks to the Watchtower maintainers and contributors for the original project.
