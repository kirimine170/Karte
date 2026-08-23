#!/usr/bin/env bash
set -euo pipefail

module_path="github.com/kirimine170/KarteRenderer"
repository_path="github.com/kirimine170/Karte_renderer"

usage() {
  cat <<'EOF'
Usage:
  ./scripts/update-karte-renderer.sh --version <immutable-version>
  ./scripts/update-karte-renderer.sh --rollback <immutable-version>

<immutable-version> must be an exact canonical SemVer tag or Go
pseudo-version. Mutable selectors such as main, master, latest, a branch name,
or a raw commit hash are rejected.
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

if [[ "$#" -ne 2 ]]; then
  usage >&2
  exit 2
fi

operation="$1"
target_version="$2"
case "$operation" in
  --version)
    action_label="Updating"
    completion_label="Updated"
    ;;
  --rollback)
    action_label="Rolling back"
    completion_label="Rolled back"
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac

# Go module versions are canonical Semantic Versions prefixed with v. Go
# pseudo-versions are canonical SemVer values as well, so exact resolution
# below distinguishes a real immutable version from a branch or revision
# selector without maintaining a second, incomplete pseudo-version grammar.
version_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$'
if [[ ! "$target_version" =~ $version_pattern ]]; then
  die "version must be an exact SemVer tag or Go pseudo-version: ${target_version}"
fi

for command_name in git go mktemp awk cmp cp rm rmdir; do
  command -v "$command_name" >/dev/null 2>&1 || die "required command is unavailable: ${command_name}"
done

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(git -C "$script_dir" rev-parse --show-toplevel 2>/dev/null)" || \
  die "the script must run from a Git checkout"
cd "$repo_root"

[[ -f go.mod ]] || die "go.mod is missing from the repository root"
[[ -f go.sum ]] || die "go.sum is missing from the repository root"

worktree_state="$(git status --porcelain=v1 --untracked-files=normal)"
if [[ -n "$worktree_state" ]]; then
  echo "$worktree_state" >&2
  die "the worktree must be clean before changing the Renderer dependency"
fi

export GOWORK=off
export GOTOOLCHAIN=local

transaction_dir=""
transaction_complete=0
placeholder_created=0
placeholder_directory_created=0
had_go_sum=1

cleanup_placeholder() {
  local cleanup_status=0
  if [[ "$placeholder_created" -eq 1 ]]; then
    rm -f frontend/dist/.placeholder || cleanup_status=1
    placeholder_created=0
  fi
  if [[ "$placeholder_directory_created" -eq 1 ]]; then
    rmdir frontend/dist 2>/dev/null || true
    placeholder_directory_created=0
  fi
  return "$cleanup_status"
}

restore_module_files() {
  local restore_status=0

  cp -p "$transaction_dir/go.mod" go.mod || restore_status=1
  if [[ "$had_go_sum" -eq 1 ]]; then
    cp -p "$transaction_dir/go.sum" go.sum || restore_status=1
  else
    rm -f go.sum || restore_status=1
  fi

  cmp -s "$transaction_dir/go.mod" go.mod || restore_status=1
  if [[ "$had_go_sum" -eq 1 ]]; then
    cmp -s "$transaction_dir/go.sum" go.sum || restore_status=1
  elif [[ -e go.sum ]]; then
    restore_status=1
  fi

  return "$restore_status"
}

cleanup_transaction_directory() {
  [[ -n "$transaction_dir" ]] || return 0
  rm -f "$transaction_dir/go.mod" "$transaction_dir/go.sum" || return 1
  rmdir "$transaction_dir"
}

finish_transaction() {
  local exit_status="$?"
  local cleanup_status=0
  trap - EXIT HUP INT TERM
  set +e

  cleanup_placeholder || cleanup_status=1
  if [[ -n "$transaction_dir" && "$transaction_complete" -ne 1 ]]; then
    echo "Restoring go.mod and go.sum after an unsuccessful Renderer update" >&2
    restore_module_files || cleanup_status=1
  fi
  cleanup_transaction_directory || cleanup_status=1

  if [[ "$cleanup_status" -ne 0 ]]; then
    echo "error: the Renderer update cleanup or restore was incomplete" >&2
    exit 125
  fi
  exit "$exit_status"
}

trap finish_transaction EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

transaction_dir="$(mktemp -d "${TMPDIR:-/tmp}/karte-renderer-update.XXXXXX")"
cp -p go.mod "$transaction_dir/go.mod"
if [[ -e go.sum ]]; then
  cp -p go.sum "$transaction_dir/go.sum"
else
  had_go_sum=0
fi

echo "Checking that the committed module files are already tidy"
if ! GOFLAGS=-mod=mod go mod tidy -diff; then
  die "go.mod or go.sum was not tidy before the Renderer update"
fi

current_state="$(
  GOFLAGS=-mod=readonly go list -m \
    -f '{{.Path}}|{{.Version}}|{{with .Replace}}{{.Path}}|{{.Version}}{{end}}' \
    "$module_path"
)" || die "failed to inspect the current Renderer dependency"
IFS='|' read -r current_path current_module_version current_repository current_version <<<"$current_state"

[[ "$current_path" == "$module_path" ]] || \
  die "unexpected Renderer module path: ${current_path:-<empty>}"
[[ "$current_repository" == "$repository_path" ]] || \
  die "the current replace target changed: ${current_repository:-<none>}"
[[ -n "$current_version" ]] || die "the current Renderer replacement has no version"

resolved_state="$(
  GOFLAGS=-mod=mod go list -m -f '{{.Version}}|{{.GoMod}}' \
    "${repository_path}@${target_version}"
)" || die "failed to resolve Renderer version ${target_version}"
IFS='|' read -r resolved_version target_go_mod <<<"$resolved_state"

[[ "$resolved_version" == "$target_version" ]] || \
  die "requested ${target_version}, but Go resolved ${resolved_version:-<empty>}"
[[ -n "$target_go_mod" && -r "$target_go_mod" ]] || \
  die "resolved Renderer version has no readable go.mod"

declared_module_path="$(
  awk '
    $1 == "module" {
      path = $2
      sub(/^"/, "", path)
      sub(/"$/, "", path)
      print path
      exit
    }
  ' "$target_go_mod"
)"
[[ "$declared_module_path" == "$module_path" ]] || \
  die "Renderer ${target_version} declares ${declared_module_path:-<no module path>}, expected ${module_path}"

echo "${action_label} Karte Renderer from ${current_version} to ${target_version}"
GOFLAGS=-mod=mod go mod edit \
  -replace="${module_path}=${repository_path}@${target_version}"
GOFLAGS=-mod=mod go mod tidy

updated_state="$(
  GOFLAGS=-mod=readonly go list -m \
    -f '{{.Path}}|{{.Version}}|{{with .Replace}}{{.Path}}|{{.Version}}{{end}}' \
    "$module_path"
)" || die "failed to inspect the updated Renderer dependency"
IFS='|' read -r updated_path updated_module_version updated_repository updated_version <<<"$updated_state"

[[ "$updated_path" == "$module_path" ]] || \
  die "updated module path changed to ${updated_path:-<empty>}"
[[ "$updated_module_version" == "$current_module_version" ]] || \
  die "the logical require version changed unexpectedly"
[[ "$updated_repository" == "$repository_path" ]] || \
  die "updated replace path changed to ${updated_repository:-<empty>}"
[[ "$updated_version" == "$target_version" ]] || \
  die "updated replacement is ${updated_version:-<empty>}, expected ${target_version}"

unexpected_paths="$(
  git diff --name-only -- . | awk '$0 != "go.mod" && $0 != "go.sum" { print }'
)"
if [[ -n "$unexpected_paths" ]]; then
  echo "$unexpected_paths" >&2
  die "dependency update changed tracked files outside go.mod and go.sum"
fi
git diff --check -- go.mod go.sum
GOFLAGS=-mod=readonly go mod verify

if [[ ! -d frontend/dist ]]; then
  mkdir -p frontend/dist
  placeholder_directory_created=1
fi
if [[ ! -e frontend/dist/.placeholder ]]; then
  : >frontend/dist/.placeholder
  placeholder_created=1
fi

echo "Testing the Renderer module"
GOFLAGS=-mod=readonly go test -count=1 "${module_path}/..."

echo "Testing the Karte Renderer contract"
GOFLAGS=-mod=readonly go test -count=1 . \
  -run 'Test(KarteRendererDependencyContractFixtures|ExportHTMLToPDFWithRendererUsesTemporaryHTMLInput)$'

echo "Testing Karte"
GOFLAGS=-mod=readonly go test -count=1 ./...

cleanup_placeholder

unexpected_paths="$(
  git diff --name-only -- . | awk '$0 != "go.mod" && $0 != "go.sum" { print }'
)"
if [[ -n "$unexpected_paths" ]]; then
  echo "$unexpected_paths" >&2
  die "tests changed tracked files outside go.mod and go.sum"
fi
git diff --check -- go.mod go.sum
GOFLAGS=-mod=readonly go mod verify

echo
echo "Validated go.mod and go.sum diff:"
git --no-pager diff -- go.mod go.sum

transaction_complete=1
echo
echo "${completion_label} Karte Renderer from ${current_version} to ${target_version}."
echo "Review and commit only go.mod and go.sum."
echo "Explicit rollback after returning to a clean worktree:"
echo "  ./scripts/update-karte-renderer.sh --rollback ${current_version}"
