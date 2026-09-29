# CLI Reference

This page is a short index. The authoritative CLI contract — invocation model,
exit codes, every command with flags, defaults, environment overrides,
authentication requirements, worked examples, the environment-variable index, and
the deprecated-surface appendix — lives in **[CLI-REFERENCE.md](CLI-REFERENCE.md)**.

Start here:

- [Invocation model and exit codes](CLI-REFERENCE.md#1-invocation-model)
- [Global conventions](CLI-REFERENCE.md#2-global-conventions)
- [Command reference](CLI-REFERENCE.md#3-command-reference)
- [Environment-variable index](CLI-REFERENCE.md#4-environment-variable-index)
- [Authentication matrix](CLI-REFERENCE.md#5-authentication-matrix)
- [Deprecated and retired surface](CLI-REFERENCE.md#6-deprecated-and-retired-surface)

For configuration keys and file formats see [CONFIGURATION.md](CONFIGURATION.md);
for MCP profiles and tools see [MCP.md](MCP.md); for the HTTP API see
[HTTP-API.md](HTTP-API.md); for server deployment see [SERVER.md](SERVER.md); for
the interactive terminal UI see [TUI-GUIDE.md](TUI-GUIDE.md).

The production entrypoint is `cmd/cortex`. Run `cortex help` for the command list
and see [CLI-REFERENCE.md](CLI-REFERENCE.md) for per-command detail.

The local v2 baseline is forward-only. `migrate down` is not a supported normal operation and existing v2 databases must not be downgraded automatically.
