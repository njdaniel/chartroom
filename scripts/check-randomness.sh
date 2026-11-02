#!/usr/bin/env bash
# Invariant 3: game randomness comes only from internal/dice.
#
# Fails if a non-test Go file imports math/rand (v1 or v2) outside
# internal/dice, or crypto/rand outside the allowlist below. crypto/rand is
# allowed only for values that are not game randomness: ids, the dice seed,
# and seat tokens. Adding a file here touches a protected path (ADR-000 item 6).
set -euo pipefail
cd "$(dirname "$0")/.."

crypto_allowed=(
  internal/dice/dice.go          # the session's dice seed
  internal/eventlog/eventlog.go  # event ids
  internal/session/ids.go        # session, seat, and token ids
)

status=0
while IFS= read -r file; do
  if grep -qE '"math/rand(/v2)?"' "$file" && [[ "$file" != internal/dice/* ]]; then
    echo "$file imports math/rand; game randomness must come from internal/dice" >&2
    status=1
  fi
  if grep -q '"crypto/rand"' "$file"; then
    ok=0
    for allowed in "${crypto_allowed[@]}"; do [ "$file" = "$allowed" ] && ok=1; done
    if [ "$ok" -eq 0 ]; then
      echo "$file imports crypto/rand; only ids, the dice seed, and seat tokens may (see scripts/check-randomness.sh)" >&2
      status=1
    fi
  fi
done < <(git ls-files --cached --others --exclude-standard '*.go' | grep -v '_test\.go$')

[ "$status" -eq 0 ] && echo "randomness: only internal/dice draws game randomness"
exit "$status"
