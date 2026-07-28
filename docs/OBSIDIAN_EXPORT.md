# Obsidian export

`cortex export --to-obsidian --vault PATH` creates a read-only projection under
`cortex/projects/`. SQLite remains the source of truth; the exporter never
imports or mutates observations.

Exports are staged and committed as one transaction. Existing files are first
renamed to same-directory backups, staged files are atomically renamed into
place, and the manifest is committed last. Any failure restores every backup.
The manifest is byte-for-byte stable for a no-op export.

Only regular, non-symlink Markdown files inside the vault are discovered.
Path components are normalized and sanitized for Windows reserved names,
invalid characters, trailing spaces/dots, traversal, case collisions, and
length limits. A renamed note is reconciled only when its recorded checksum
still matches; edited renamed notes are left untouched and reported as a
conflict. Duplicate `cortex_id` values are errors and include both paths.

Frontmatter is YAML-escaped and related observations use deterministic
wikilinks. Personal/private observations are excluded unless explicitly opted
in, with a warning.
