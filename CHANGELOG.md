# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## Unreleased

### Security

- Upgrade gRPC to v1.82.1 and `golang.org/x/text` to v0.39.0 to remediate reachable vulnerabilities.
- Require Go toolchain 1.26.5 across modules, CI, release builds, and Docker builds.
