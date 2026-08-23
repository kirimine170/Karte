#!/usr/bin/env bash

set -euo pipefail

repository_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/karte-startup-smoke-script-test.XXXXXX")

cleanup() {
  rm -rf -- "$fixture_root"
}
trap cleanup EXIT

mkdir -p \
  "$fixture_root/tools" \
  "$fixture_root/temp" \
  "$fixture_root/gocache" \
  "$fixture_root/success payload" \
  "$fixture_root/failure payload" \
  "$fixture_root/timeout payload"

printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'while (($#)); do' \
  '  case "$1" in' \
  '    -a|--server-args=*) shift ;;' \
  '    *) exec "$@" ;;' \
  '  esac' \
  'done' \
  'exit 2' \
  >"$fixture_root/tools/xvfb-run"
chmod +x "$fixture_root/tools/xvfb-run"

printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  '[[ ${KARTE_DATA_DIR:-} == /* ]]' \
  '[[ ${KARTE_STARTUP_SMOKE_READY_FILE:-} == /* ]]' \
  '[[ -z ${LD_LIBRARY_PATH+x} ]]' \
  '[[ ! -e $KARTE_STARTUP_SMOKE_READY_FILE ]]' \
  'temporary_marker="${KARTE_STARTUP_SMOKE_READY_FILE}.temporary"' \
  "printf 'karte-dom-ready-v1\\n' >\"\$temporary_marker\"" \
  'ln "$temporary_marker" "$KARTE_STARTUP_SMOKE_READY_FILE"' \
  'rm "$temporary_marker"' \
  >"$fixture_root/success payload/karte"
chmod +x "$fixture_root/success payload/karte"

printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'sleep 30 &' \
  'child=$!' \
  'printf "%s\\n" "$child" >"$KARTE_DATA_DIR/child.log"' \
  'exit 7' \
  >"$fixture_root/failure payload/karte"
chmod +x "$fixture_root/failure payload/karte"

printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  'sleep 30 &' \
  'child=$!' \
  'printf "%s\\n" "$child" >"$KARTE_DATA_DIR/child.log"' \
  'wait "$child"' \
  >"$fixture_root/timeout payload/karte"
chmod +x "$fixture_root/timeout payload/karte"

(
  cd "$fixture_root/success payload"
  zip -qry "$fixture_root/success artifact.zip" .
)
(
  cd "$fixture_root/failure payload"
  zip -qry "$fixture_root/failure artifact.zip" .
)
(
  cd "$fixture_root/timeout payload"
  zip -qry "$fixture_root/timeout artifact.zip" .
)

cd "$repository_root"
PATH="$fixture_root/tools:$PATH" RUNNER_TEMP="$fixture_root/temp" GOCACHE="$fixture_root/gocache" LD_LIBRARY_PATH="$fixture_root/forbidden" \
  ./scripts/smoke-desktop-artifact.sh \
    linux \
    "$fixture_root/success artifact.zip" \
    "$fixture_root/success logs" \
    10

grep -Fq 'artifact extraction passed' "$fixture_root/success logs/extraction.log"
grep -Fxq 'target=linux' "$fixture_root/success logs/artifact-layout.txt"

set +e
PATH="$fixture_root/tools:$PATH" RUNNER_TEMP="$fixture_root/temp" GOCACHE="$fixture_root/gocache" \
  ./scripts/smoke-desktop-artifact.sh \
    linux \
    "$fixture_root/failure artifact.zip" \
    "$fixture_root/failure logs" \
    10
failure_status=$?
set -e
if [[ $failure_status -eq 0 ]]; then
  echo "failure smoke unexpectedly passed" >&2
  exit 1
fi

failure_child_log=$(find "$fixture_root/failure logs" -type f -name '*child.log' -print -quit)
if [[ -z "$failure_child_log" ]]; then
  echo "failure child diagnostic was not collected" >&2
  exit 1
fi
failure_child_pid=$(tr -d '[:space:]' <"$failure_child_log")
if kill -0 "$failure_child_pid" 2>/dev/null; then
  echo "failure left child process running: $failure_child_pid" >&2
  exit 1
fi

set +e
PATH="$fixture_root/tools:$PATH" RUNNER_TEMP="$fixture_root/temp" GOCACHE="$fixture_root/gocache" \
  ./scripts/smoke-desktop-artifact.sh \
    linux \
    "$fixture_root/timeout artifact.zip" \
    "$fixture_root/timeout logs" \
    1
timeout_status=$?
set -e
if [[ $timeout_status -ne 124 ]]; then
  echo "timeout smoke status=${timeout_status}，want 124" >&2
  exit 1
fi

child_log=$(find "$fixture_root/timeout logs" -type f -name '*child.log' -print -quit)
if [[ -z "$child_log" ]]; then
  echo "timeout child diagnostic was not collected" >&2
  exit 1
fi
child_pid=$(tr -d '[:space:]' <"$child_log")
if kill -0 "$child_pid" 2>/dev/null; then
  echo "timeout left child process running: $child_pid" >&2
  exit 1
fi

set +e
./scripts/smoke-desktop-artifact.sh \
  linux \
  "$fixture_root/success artifact.zip" \
  "$fixture_root/extra argument logs" \
  10 \
  unexpected
extra_argument_status=$?
set -e
if [[ $extra_argument_status -ne 2 ]]; then
  echo "extra argument status=${extra_argument_status}，want 2" >&2
  exit 1
fi

echo "desktop artifact smoke script tests passed"
