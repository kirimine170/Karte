#!/usr/bin/env bash

set -euo pipefail

target=${1:?usage: smoke-desktop-artifact.sh TARGET ARCHIVE LOG_DIRECTORY [TIMEOUT_SECONDS]}
archive_input=${2:?usage: smoke-desktop-artifact.sh TARGET ARCHIVE LOG_DIRECTORY [TIMEOUT_SECONDS]}
log_directory_input=${3:?usage: smoke-desktop-artifact.sh TARGET ARCHIVE LOG_DIRECTORY [TIMEOUT_SECONDS]}
timeout_seconds=${4:-60}
if (($# > 4)); then
  echo "too many arguments" >&2
  exit 2
fi

case "$target" in
  darwin-arm64|darwin-amd64|linux)
    ;;
  *)
    echo "unsupported Unix desktop smoke target: $target" >&2
    exit 2
    ;;
esac

case "$timeout_seconds" in
  ''|*[!0-9]*)
    echo "TIMEOUT_SECONDS must be a positive integer" >&2
    exit 2
    ;;
esac
if ((timeout_seconds <= 0)); then
  echo "TIMEOUT_SECONDS must be a positive integer" >&2
  exit 2
fi

archive_directory=$(cd "$(dirname "$archive_input")" && pwd -P)
archive_path="$archive_directory/$(basename "$archive_input")"
if [[ ! -f "$archive_path" ]]; then
  echo "artifact archive not found: $archive_path" >&2
  exit 1
fi

mkdir -p "$log_directory_input"
log_directory=$(cd "$log_directory_input" && pwd -P)
temporary_parent=${RUNNER_TEMP:-${TMPDIR:-/tmp}}
mkdir -p "$temporary_parent"
temporary_parent=$(cd "$temporary_parent" && pwd -P)
smoke_root=$(mktemp -d "$temporary_parent/karte-startup-smoke-$target.XXXXXX")
extract_root="$smoke_root/extracted artifact"

collect_data_logs() {
  local data_directory
  local label
  local log_file
  local copied=0
  for data_directory in "$smoke_root"/data-*; do
    [[ -d "$data_directory" ]] || continue
    label=$(basename "$data_directory")
    while IFS= read -r -d '' log_file; do
      copied=$((copied + 1))
      cp -p "$log_file" "$log_directory/${label}-${copied}-$(basename "$log_file")" || true
    done < <(find "$data_directory" -type f \( -name '*.log' -o -name '*.jsonl' \) -print0 2>/dev/null)
  done
  {
    echo "target=$target"
    echo "archive=$archive_path"
    echo "smoke_root=$smoke_root"
    find "$extract_root" -maxdepth 5 -print 2>/dev/null | sort | head -500
  } >"$log_directory/artifact-layout.txt" || true
}

cleanup() {
  local status=$?
  set +e
  collect_data_logs
  rm -rf -- "$smoke_root"
  return "$status"
}
trap cleanup EXIT

go run ./cmd/artifactsmoke \
  -archive "$archive_path" \
  -destination "$extract_root" \
  >"$log_directory/extraction.log" 2>&1

terminate_process_tree() {
  local process_id=$1
  local signal=${2:-TERM}
  local children
  children=$(pgrep -P "$process_id" 2>/dev/null || true)
  local child
  for child in $children; do
    terminate_process_tree "$child" "$signal"
  done
  kill -"$signal" "$process_id" 2>/dev/null || true
}

signal_process_group() {
  local process_group_id=$1
  local signal=$2
  kill -"$signal" -- "-$process_group_id" 2>/dev/null || true
}

process_group_is_alive() {
  local process_group_id=$1
  kill -0 -- "-$process_group_id" 2>/dev/null
}

wait_for_process_group_exit() {
  local process_group_id=$1
  local attempts=${2:-20}
  local attempt
  for ((attempt = 0; attempt < attempts; attempt++)); do
    if ! process_group_is_alive "$process_group_id"; then
      return 0
    fi
    sleep 0.1
  done
  ! process_group_is_alive "$process_group_id"
}

terminate_process_group() {
  local process_group_id=$1
  signal_process_group "$process_group_id" TERM
  if wait_for_process_group_exit "$process_group_id" 20; then
    return 0
  fi
  signal_process_group "$process_group_id" KILL
  wait_for_process_group_exit "$process_group_id" 30
}

matching_smoke_process_ids() {
  local executable_path=$1
  local marker_path=$2
  local marker_environment="KARTE_STARTUP_SMOKE_READY_FILE=$marker_path"
  local process_id
  local command
  while read -r process_id command; do
    [[ -n "$process_id" ]] || continue
    if [[ "$command" == *"$marker_environment"* || "$command" == "$executable_path" || "$command" == "$executable_path "* ]]; then
      printf '%s\n' "$process_id"
    fi
  done < <(ps eww -axo pid=,command= 2>/dev/null || true)
}

terminate_smoke_processes() {
  local executable_path=$1
  local marker_path=$2
  local signal=${3:-TERM}
  local process_id
  while IFS= read -r process_id; do
    [[ -n "$process_id" ]] || continue
    terminate_process_tree "$process_id" "$signal"
  done < <(matching_smoke_process_ids "$executable_path" "$marker_path")
}

record_process_tree() {
  local process_id=$1
  local tracker=$2
  local command
  command=$(ps -p "$process_id" -o command= 2>/dev/null || true)
  if [[ -n "$command" ]]; then
    printf '%s\t%s\n' "$process_id" "$command" >>"$tracker"
  fi
  local children
  children=$(pgrep -P "$process_id" 2>/dev/null || true)
  local child
  for child in $children; do
    record_process_tree "$child" "$tracker"
  done
}

record_smoke_processes() {
  local executable_path=$1
  local marker_path=$2
  local tracker=$3
  local process_id
  while IFS= read -r process_id; do
    [[ -n "$process_id" ]] || continue
    record_process_tree "$process_id" "$tracker"
  done < <(matching_smoke_process_ids "$executable_path" "$marker_path")
}

terminate_tracked_processes() {
  local tracker=$1
  local signal=$2
  local terminated=0
  local process_id
  local expected_command
  local current_command
  [[ -f "$tracker" ]] || {
    printf '0\n'
    return
  }
  while IFS=$'\t' read -r process_id expected_command; do
    [[ -n "$process_id" && -n "$expected_command" ]] || continue
    current_command=$(ps -p "$process_id" -o command= 2>/dev/null || true)
    if [[ "$current_command" == "$expected_command" ]]; then
      terminate_process_tree "$process_id" "$signal"
      terminated=$((terminated + 1))
    fi
  done < <(sort -u "$tracker")
  printf '%s\n' "$terminated"
}

wait_for_smoke_process() {
  local label=$1
  local process_id=$2
  local process_group_id=$3
  local marker_path=$4
  local executable_match=${5:-}
  local process_tracker="$smoke_root/$label-processes.tsv"
  : >"$process_tracker"
  local deadline=$((SECONDS + timeout_seconds))
  local timed_out=0

  while kill -0 "$process_id" 2>/dev/null; do
    record_process_tree "$process_id" "$process_tracker"
    if [[ -n "$executable_match" ]]; then
      record_smoke_processes "$executable_match" "$marker_path" "$process_tracker"
    fi
    if ((SECONDS >= deadline)); then
      timed_out=1
      {
        echo "timeout after ${timeout_seconds}s: label=$label pid=$process_id"
        ps -axo pid=,ppid=,state=,etime=,command= || true
      } >"$log_directory/$label-timeout-processes.log" 2>&1
      signal_process_group "$process_group_id" TERM
      if [[ -n "$executable_match" ]]; then
        terminate_smoke_processes "$executable_match" "$marker_path" TERM
      fi
      sleep 2
      signal_process_group "$process_group_id" KILL
      if [[ -n "$executable_match" ]]; then
        terminate_smoke_processes "$executable_match" "$marker_path" KILL
      fi
      break
    fi
    sleep 0.2
  done

  set +e
  wait "$process_id"
  local exit_code=$?
  set -e
  if [[ -n "$executable_match" ]]; then
    record_smoke_processes "$executable_match" "$marker_path" "$process_tracker"
  fi
  local group_remaining=0
  if process_group_is_alive "$process_group_id"; then
    group_remaining=1
    if ! terminate_process_group "$process_group_id"; then
      echo "$label process group $process_group_id did not terminate" >&2
      return 1
    fi
  fi
  local remaining
  remaining=$(terminate_tracked_processes "$process_tracker" TERM)
  if ((remaining > 0)); then
    sleep 1
    terminate_tracked_processes "$process_tracker" KILL >/dev/null
    if ((timed_out == 0)); then
      echo "$label left $remaining tracked child process(es) after exit" >&2
      return 1
    fi
  fi
  if ((group_remaining > 0 && timed_out == 0)); then
    echo "$label left processes in group $process_group_id after exit" >&2
    return 1
  fi
  if ((timed_out)); then
    echo "$label timed out" >&2
    return 124
  fi
  if ((exit_code != 0)); then
    echo "$label exited with code $exit_code" >&2
    return "$exit_code"
  fi
  if [[ ! -f "$marker_path" ]]; then
    echo "$label exited without a DOM-ready marker: $marker_path" >&2
    return 1
  fi
  if ! grep -Fxq 'karte-dom-ready-v1' "$marker_path"; then
    echo "$label wrote an invalid DOM-ready marker" >&2
    return 1
  fi
}

run_direct_smoke() {
  local label=$1
  local executable=$2
  local working_directory=$3
  local data_directory="$smoke_root/data-$label"
  local marker_directory="$smoke_root/markers-$label"
  local marker_path="$marker_directory/DOM ready.marker"
  mkdir -p "$data_directory" "$marker_directory"

  set -m
  (
    cd "$working_directory"
    env \
      -u DYLD_LIBRARY_PATH \
      -u DYLD_FALLBACK_LIBRARY_PATH \
      -u DYLD_FRAMEWORK_PATH \
      -u DYLD_FALLBACK_FRAMEWORK_PATH \
      -u DYLD_INSERT_LIBRARIES \
      -u LD_LIBRARY_PATH \
      KARTE_DATA_DIR="$data_directory" \
      KARTE_STARTUP_SMOKE_READY_FILE="$marker_path" \
      "$executable"
  ) >"$log_directory/$label-stdout.log" 2>"$log_directory/$label-stderr.log" &
  local process_id=$!
  set +m
  wait_for_smoke_process "$label" "$process_id" "$process_id" "$marker_path" "$executable"
}

if [[ "$target" == "linux" ]]; then
  executable="$extract_root/karte"
  if [[ ! -x "$executable" ]]; then
    echo "extracted Linux executable is missing or not executable: $executable" >&2
    exit 1
  fi
  if ! command -v xvfb-run >/dev/null 2>&1; then
    echo "xvfb-run is required for Linux startup smoke" >&2
    exit 1
  fi
  data_directory="$smoke_root/data-linux"
  marker_directory="$smoke_root/markers-linux"
  marker_path="$marker_directory/DOM ready.marker"
  mkdir -p "$data_directory" "$marker_directory"
  set -m
  (
    cd "$extract_root"
    env \
      -u LD_LIBRARY_PATH \
      KARTE_DATA_DIR="$data_directory" \
      KARTE_STARTUP_SMOKE_READY_FILE="$marker_path" \
      xvfb-run -a --server-args='-screen 0 1280x800x24' "$executable"
  ) >"$log_directory/linux-stdout.log" 2>"$log_directory/linux-stderr.log" &
  process_id=$!
  set +m
  wait_for_smoke_process linux "$process_id" "$process_id" "$marker_path" "$executable"
  exit 0
fi

app_bundle="$extract_root/$target/Karte.app"
info_plist="$app_bundle/Contents/Info.plist"
if [[ ! -d "$app_bundle" || ! -f "$info_plist" ]]; then
  echo "extracted macOS app bundle is missing: $app_bundle" >&2
  exit 1
fi
executable_name=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$info_plist")
if [[ -z "$executable_name" || "$executable_name" == "." || "$executable_name" == ".." || "$executable_name" == */* || "$executable_name" == *\\* ]]; then
  echo "macOS CFBundleExecutable is not a simple file name: $executable_name" >&2
  exit 1
fi
executable="$app_bundle/Contents/MacOS/$executable_name"
if [[ ! -x "$executable" ]]; then
  echo "extracted macOS executable is missing or not executable: $executable" >&2
  exit 1
fi

run_direct_smoke macos-direct "$executable" "$(dirname "$app_bundle")"

quarantine_value="0081;$(printf '%x' "$(date +%s)");KarteCI;T-082"
xattr -r -w com.apple.quarantine "$quarantine_value" "$app_bundle"
recorded_quarantine=$(xattr -p com.apple.quarantine "$app_bundle")
if [[ "$recorded_quarantine" != "$quarantine_value" ]]; then
  echo "failed to persist quarantine xattr on extracted app" >&2
  exit 1
fi
printf '%s\n' "$recorded_quarantine" >"$log_directory/macos-quarantine-xattr.log"

data_directory="$smoke_root/data-macos-quarantine"
marker_directory="$smoke_root/markers-macos-quarantine"
marker_path="$marker_directory/DOM ready.marker"
mkdir -p "$data_directory" "$marker_directory"
set -m
env \
  -u DYLD_LIBRARY_PATH \
  -u DYLD_FALLBACK_LIBRARY_PATH \
  -u DYLD_FRAMEWORK_PATH \
  -u DYLD_FALLBACK_FRAMEWORK_PATH \
  -u DYLD_INSERT_LIBRARIES \
  -u LD_LIBRARY_PATH \
  /usr/bin/open -W -n -F -j \
  --stdout "$log_directory/macos-quarantine-app-stdout.log" \
  --stderr "$log_directory/macos-quarantine-app-stderr.log" \
  --env "KARTE_DATA_DIR=$data_directory" \
  --env "KARTE_STARTUP_SMOKE_READY_FILE=$marker_path" \
  "$app_bundle" \
  >"$log_directory/macos-quarantine-open.log" 2>&1 &
process_id=$!
set +m
wait_for_smoke_process macos-quarantine "$process_id" "$process_id" "$marker_path" "$executable"
