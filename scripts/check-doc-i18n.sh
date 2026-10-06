#!/usr/bin/env bash
# Advisory i18n coverage report: lists English docs pages without an adjacent
# <name>.es.md sibling (mkdocs-static-i18n suffix mode). Always exits 0 so CI
# can run it as a non-blocking status step.

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
docs_dir="$repo_root/docs"

translated=0
missing=0
missing_pages=()

if [ -d "$docs_dir" ]; then
  while IFS= read -r page; do
    case "$(basename -- "$page")" in
      *.es.md | '._'* | README.md | TRANSLATING.md) continue ;;
    esac
    sibling="${page%.md}.es.md"
    if [ -f "$sibling" ]; then
      translated=$((translated + 1))
    else
      missing=$((missing + 1))
      missing_pages+=("${page#"$repo_root"/}")
    fi
  done < <(find "$docs_dir" -type f -name '*.md' ! -name '._*' | LC_ALL=C sort)
fi

for page in ${missing_pages[@]+"${missing_pages[@]}"}; do
  echo "missing: ${page%.md}.es.md"
done

total=$((translated + missing))
echo "docs i18n coverage: ${translated}/${total} pages have a .es.md sibling (${missing} missing)"
exit 0
