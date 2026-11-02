#!/usr/bin/env bash
# Fails if a direct dependency in go.mod or web/package.json is not in
# deps-allowlist.txt (ADR-000 item 2, ADR-001).
set -euo pipefail
cd "$(dirname "$0")/.."

allowed=$(grep -vE '^\s*(#|$)' deps-allowlist.txt | sed 's/[[:space:]]*$//')

go_deps=$(go list -m -f '{{if and (not .Main) (not .Indirect)}}{{.Path}}{{end}}' all)
npm_deps=$(node -e '
  const p = require("./web/package.json");
  for (const name of Object.keys({ ...p.dependencies, ...p.devDependencies })) console.log(name);
')

status=0
for dep in $go_deps $npm_deps; do
  if ! grep -qxF "$dep" <<<"$allowed"; then
    echo "not in deps-allowlist.txt: $dep (new dependencies need Nick's sign-off)" >&2
    status=1
  fi
done
[ "$status" -eq 0 ] && echo "deps: all direct dependencies are approved"
exit "$status"
