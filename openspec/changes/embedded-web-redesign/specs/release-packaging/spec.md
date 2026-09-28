# Delta for release-packaging

## ADDED Requirements

### Requirement: REQ-REL-001: release build ordering for embedded assets
GoReleaser and the release workflow MUST build and sync the Next.js static export into `internal/web/dist` before Go compilation, so published artifacts and the six release targets carry the real UI while `goreleaser check`/CI YAML validity stay green.

#### Scenario: release pipeline order (Happy path)
- GIVEN a push to the release branch
- WHEN the release job executes
- THEN web assets are embedded in every produced binary before packaging/signing steps

#### Scenario: cache reuse (Edge case)
- GIVEN unchanged `web/` sources between builds
- WHEN the build runs again
- THEN the sync step is idempotent and the embedded digest is deterministic for identical bytes

#### Scenario: web build failure stops release (Error state)
- GIVEN the export build fails
- WHEN the ordering step runs
- THEN the release aborts before any Go artifact is produced or published

### Requirement: REQ-REL-002: cortex-web image retirement
The separate `cortex-web` Docker image MUST be retired: `docker/Dockerfile.web` deleted, web services removed from `docker-compose.yml` and `docker/docker-compose.prod.yml`, release image publishing for cortex-web removed, and the docker_e2e compose test updated to health-check the embedded UI through the cortex container.

#### Scenario: compose stack serves UI (Happy path)
- GIVEN the updated compose files
- WHEN `docker compose up` runs
- THEN only the cortex image starts and the web UI answers on its published port

#### Scenario: e2e health expectation migrated (Edge case)
- GIVEN `e2e/docker_compose_test.go` previously probed a standalone web service
- WHEN the tag-gated e2e suite runs in CI
- THEN it probes the embedded surface instead and passes

#### Scenario: stale reference remains (Error state)
- GIVEN any workflow or doc still references the cortex-web image after the wave
- WHEN reviewers grep the tree
- THEN the leftover is treated as a task FAIL with the reference named
