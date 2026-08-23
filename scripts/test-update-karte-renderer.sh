#!/usr/bin/env bash
set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
subject="$script_dir/update-karte-renderer.sh"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/karte-renderer-update-self-test.XXXXXX")"
: >"$test_root/.owned-by-renderer-update-self-test"

cleanup() {
  if [[ -n "${test_root:-}" && -f "$test_root/.owned-by-renderer-update-self-test" ]]; then
    rm -rf "$test_root"
  fi
}
trap cleanup EXIT HUP INT TERM

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  local file="$1"
  local expected="$2"
  grep -Fq -- "$expected" "$file" || \
    fail "expected '$expected' in $file"
}

assert_files_equal() {
  local expected="$1"
  local actual="$2"
  cmp -s "$expected" "$actual" || \
    fail "$actual was not restored byte-for-byte"
}

fixture_dir=""
fixture_output=""
fixture_log=""
run_status=0

make_fixture() {
  local name="$1"
  local current_version="${2:-v0.0.0-20260801160039-ede38ba276cd}"
  local declared_module="${3:-github.com/kirimine170/KarteRenderer}"

  fixture_dir="$test_root/$name"
  fixture_output="$test_root/$name.output"
  fixture_log="$test_root/$name.go.log"
  mkdir -p "$fixture_dir/scripts" "$fixture_dir/fake-bin" \
    "$fixture_dir/fixtures" "$fixture_dir/frontend"
  cp "$subject" "$fixture_dir/scripts/update-karte-renderer.sh"
  chmod +x "$fixture_dir/scripts/update-karte-renderer.sh"

  printf '%s\n' \
    'module karte' \
    '' \
    'go 1.25.0' \
    '' \
    'require github.com/kirimine170/KarteRenderer v0.0.0' \
    '' \
    "replace github.com/kirimine170/KarteRenderer => github.com/kirimine170/Karte_renderer ${current_version}" \
    >"$fixture_dir/go.mod"
  printf '%s\n' 'original go.sum bytes' >"$fixture_dir/go.sum"
  printf 'module %s\n\ngo 1.22\n' "$declared_module" \
    >"$fixture_dir/fixtures/renderer.mod"
  : >"$fixture_dir/frontend/.gitkeep"

  cat >"$fixture_dir/fake-bin/go" <<'FAKE_GO'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >>"$FAKE_GO_LOG"

module_path="github.com/kirimine170/KarteRenderer"
repository_path="github.com/kirimine170/Karte_renderer"
fail_stage="${FAKE_FAIL_STAGE:-}"

if [[ "$1" == "mod" && "$2" == "tidy" && "${3:-}" == "-diff" ]]; then
  [[ "$fail_stage" != "initial_tidy" ]] || exit 31
  exit 0
fi

if [[ "$1" == "list" && "$2" == "-m" ]]; then
  last_argument="${!#}"
  if [[ "$last_argument" == *"@"* ]]; then
    requested_version="${last_argument##*@}"
    printf '%s|%s\n' "${FAKE_RESOLVED_VERSION:-$requested_version}" "$FAKE_TARGET_MOD"
    exit 0
  fi

  replacement="$(
    awk -v module="$module_path" \
      '$1 == "replace" && $2 == module && $3 == "=>" {
        if ($5 != "") {
          print $4 "@" $5
        } else {
          print $4
        }
        exit
      }' go.mod
  )"
  replacement_path="${replacement%@*}"
  replacement_version="${replacement##*@}"
  printf '%s|v0.0.0|%s|%s\n' \
    "$module_path" "$replacement_path" "$replacement_version"
  exit 0
fi

if [[ "$1" == "mod" && "$2" == "edit" ]]; then
  [[ "$fail_stage" != "edit" ]] || exit 32
  replacement_argument="${3#-replace=}"
  replacement_module="${replacement_argument%%=*}"
  replacement_target="${replacement_argument#*=}"
  replacement_path="${replacement_target%@*}"
  replacement_version="${replacement_target##*@}"
  awk -v module="$replacement_module" -v path="$replacement_path" \
    -v version="$replacement_version" '
    $1 == "replace" && $2 == module && $3 == "=>" {
      print "replace " module " => " path " " version
      replaced = 1
      next
    }
    { print }
    END {
      if (!replaced) {
        print "replace " module " => " path " " version
      }
    }
  ' go.mod >go.mod.fake-update
  mv go.mod.fake-update go.mod
  exit 0
fi

if [[ "$1" == "mod" && "$2" == "tidy" ]]; then
  if [[ "$fail_stage" == "tidy" ]]; then
    printf '%s\n' '// mutation before tidy failure' >>go.mod
    printf '%s\n' 'partial sum' >go.sum
    exit 33
  fi
  replacement_version="$(awk '$1 == "replace" { print $5; exit }' go.mod)"
  printf 'renderer %s\n' "$replacement_version" >go.sum
  exit 0
fi

if [[ "$1" == "mod" && "$2" == "verify" ]]; then
  [[ "$fail_stage" != "verify" ]] || exit 34
  exit 0
fi

if [[ "$1" == "test" ]]; then
  arguments=" $* "
  if [[ "$fail_stage" == "renderer_test" && "$arguments" == *" ${module_path}/... "* ]]; then
    exit 35
  fi
  if [[ "$fail_stage" == "contract_test" && "$arguments" == *" Test(KarteRendererDependencyContractFixtures"* ]]; then
    exit 36
  fi
  if [[ "$fail_stage" == "full_test" && "$arguments" == *" ./... "* ]]; then
    exit 37
  fi
  exit 0
fi

echo "unexpected fake go invocation: $*" >&2
exit 99
FAKE_GO
  chmod +x "$fixture_dir/fake-bin/go"

  git -C "$fixture_dir" init -q
  git -C "$fixture_dir" config user.name "Renderer update self-test"
  git -C "$fixture_dir" config user.email "renderer-update-self-test@example.invalid"
  git -C "$fixture_dir" add .
  git -C "$fixture_dir" commit -qm "fixture baseline"
}

snapshot_module_files() {
  cp "$fixture_dir/go.mod" "$fixture_dir.before.go.mod"
  cp "$fixture_dir/go.sum" "$fixture_dir.before.go.sum"
}

assert_module_files_restored() {
  assert_files_equal "$fixture_dir.before.go.mod" "$fixture_dir/go.mod"
  assert_files_equal "$fixture_dir.before.go.sum" "$fixture_dir/go.sum"
}

run_subject() {
  local fail_stage="${1:-}"
  local resolved_version="${2:-}"
  shift 2 || true

  set +e
  (
    cd "$fixture_dir"
    PATH="$fixture_dir/fake-bin:$PATH" \
      FAKE_GO_LOG="$fixture_log" \
      FAKE_TARGET_MOD="$fixture_dir/fixtures/renderer.mod" \
      FAKE_FAIL_STAGE="$fail_stage" \
      FAKE_RESOLVED_VERSION="$resolved_version" \
      ./scripts/update-karte-renderer.sh "$@"
  ) >"$fixture_output" 2>&1
  run_status="$?"
  set -e
}

test_explicit_version_is_required() {
  make_fixture explicit-version
  snapshot_module_files
  run_subject "" ""
  [[ "$run_status" -ne 0 ]] || fail "missing version unexpectedly succeeded"
  assert_module_files_restored
  assert_contains "$fixture_output" "Usage:"
}

test_mutable_selectors_are_rejected() {
  local selector
  for selector in main master latest HEAD @main agent/feature ede38ba276cd; do
    make_fixture "mutable-${selector//\//-}"
    snapshot_module_files
    run_subject "" "" --version "$selector"
    [[ "$run_status" -ne 0 ]] || fail "mutable selector unexpectedly succeeded: $selector"
    assert_module_files_restored
    assert_contains "$fixture_output" "exact SemVer tag or Go pseudo-version"
  done
}

test_semver_update_succeeds() {
  make_fixture semver-success
  run_subject "" "" --version v0.2.0
  [[ "$run_status" -eq 0 ]] || fail "SemVer update failed: $(cat "$fixture_output")"
  assert_contains "$fixture_dir/go.mod" \
    "replace github.com/kirimine170/KarteRenderer => github.com/kirimine170/Karte_renderer v0.2.0"
  assert_contains "$fixture_dir/go.sum" "renderer v0.2.0"
  assert_contains "$fixture_log" \
    "Test(KarteRendererDependencyContractFixtures|ExportHTMLToPDFWithRendererUsesTemporaryHTMLInput)"
  assert_contains "$fixture_output" \
    "./scripts/update-karte-renderer.sh --rollback v0.0.0-20260801160039-ede38ba276cd"
  [[ ! -e "$fixture_dir/frontend/dist/.placeholder" ]] || \
    fail "temporary frontend placeholder was left behind"
}

test_pseudo_version_update_succeeds() {
  local pseudo="v0.0.0-20260816031944-738a366b22ba"
  make_fixture pseudo-success
  run_subject "" "" --version "$pseudo"
  [[ "$run_status" -eq 0 ]] || fail "pseudo-version update failed: $(cat "$fixture_output")"
  assert_contains "$fixture_dir/go.mod" "github.com/kirimine170/Karte_renderer ${pseudo}"
}

test_resolution_mismatch_restores_files() {
  make_fixture resolution-mismatch
  snapshot_module_files
  run_subject "" "v0.2.1" --version v0.2.0
  [[ "$run_status" -ne 0 ]] || fail "resolution mismatch unexpectedly succeeded"
  assert_module_files_restored
  assert_contains "$fixture_output" "requested v0.2.0, but Go resolved v0.2.1"
}

test_module_path_mismatch_restores_files() {
  make_fixture module-mismatch \
    v0.0.0-20260801160039-ede38ba276cd \
    github.com/kirimine170/Karte_renderer
  snapshot_module_files
  run_subject "" "" --version v0.2.0
  [[ "$run_status" -ne 0 ]] || fail "module path mismatch unexpectedly succeeded"
  assert_module_files_restored
  assert_contains "$fixture_output" "declares github.com/kirimine170/Karte_renderer"
}

test_tidy_failure_restores_files() {
  make_fixture tidy-failure
  snapshot_module_files
  run_subject tidy "" --version v0.2.0
  [[ "$run_status" -ne 0 ]] || fail "tidy failure unexpectedly succeeded"
  assert_module_files_restored
  assert_contains "$fixture_output" "Restoring go.mod and go.sum"
}

test_contract_failure_restores_files() {
  make_fixture contract-failure
  snapshot_module_files
  run_subject contract_test "" --version v0.2.0
  [[ "$run_status" -ne 0 ]] || fail "contract failure unexpectedly succeeded"
  assert_module_files_restored
  assert_contains "$fixture_output" "Testing the Karte Renderer contract"
  [[ ! -e "$fixture_dir/frontend/dist/.placeholder" ]] || \
    fail "temporary frontend placeholder survived a failed transaction"
}

test_other_post_update_failures_restore_files() {
  local stage
  for stage in verify renderer_test full_test; do
    make_fixture "${stage}-failure"
    snapshot_module_files
    run_subject "$stage" "" --version v0.2.0
    [[ "$run_status" -ne 0 ]] || fail "${stage} failure unexpectedly succeeded"
    assert_module_files_restored
    assert_contains "$fixture_output" "Restoring go.mod and go.sum"
    [[ ! -e "$fixture_dir/frontend/dist/.placeholder" ]] || \
      fail "temporary frontend placeholder survived ${stage} failure"
  done
}

test_explicit_rollback_uses_the_same_gates() {
  make_fixture rollback v0.2.0
  run_subject "" "" --rollback v0.1.1
  [[ "$run_status" -eq 0 ]] || fail "explicit rollback failed: $(cat "$fixture_output")"
  assert_contains "$fixture_dir/go.mod" \
    "replace github.com/kirimine170/KarteRenderer => github.com/kirimine170/Karte_renderer v0.1.1"
  assert_contains "$fixture_log" \
    "Test(KarteRendererDependencyContractFixtures|ExportHTMLToPDFWithRendererUsesTemporaryHTMLInput)"
  assert_contains "$fixture_output" "Rolled back Karte Renderer from v0.2.0 to v0.1.1"
}

test_explicit_version_is_required
test_mutable_selectors_are_rejected
test_semver_update_succeeds
test_pseudo_version_update_succeeds
test_resolution_mismatch_restores_files
test_module_path_mismatch_restores_files
test_tidy_failure_restores_files
test_contract_failure_restores_files
test_other_post_update_failures_restore_files
test_explicit_rollback_uses_the_same_gates

echo "PASS: update-karte-renderer transactional self-test"
