#!/usr/bin/env bash
# Checks that the local toolchain meets ARGUS minimum versions.
set -u
ok=0; fail=0
check () {
  local name="$1" cmd="$2" min="$3"
  if command -v "$cmd" >/dev/null 2>&1; then
    local v; v="$($cmd --version 2>&1 | head -1)"
    printf "  %-14s found: %s\n" "$name" "$v"
    ok=$((ok+1))
  else
    printf "  %-14s MISSING (need >= %s)\n" "$name" "$min"
    fail=$((fail+1))
  fi
}
echo "ARGUS toolchain check"
check "go"            go            "1.26"
check "node"          node          "22"
check "pnpm"          pnpm          "9"
check "docker"        docker        "24"
check "git"           git           "2.40"
check "golangci-lint" golangci-lint "2.0"
check "sqlc"          sqlc          "1.27"
check "oapi-codegen"  oapi-codegen  "2.4"
check "migrate"       migrate       "4.17"
echo
echo "found: $ok, missing: $fail"
if [ "$fail" -gt 0 ]; then
  echo "Install missing Go tools with: make tools"
  echo "Install sqlc/oapi/migrate are Go tools; node+pnpm+docker install from their sites."
fi
