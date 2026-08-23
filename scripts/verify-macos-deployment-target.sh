#!/usr/bin/env bash

set -euo pipefail

app_bundle=${1:?usage: verify-macos-deployment-target.sh APP_BUNDLE SUPPORTED_VERSION}
supported_version=${2:?usage: verify-macos-deployment-target.sh APP_BUNDLE SUPPORTED_VERSION}

file_bin=${FILE_BIN:-file}
find_bin=${FIND_BIN:-find}
lipo_bin=${LIPO_BIN:-lipo}
plistbuddy_bin=${PLISTBUDDY_BIN:-/usr/libexec/PlistBuddy}
realpath_bin=${REALPATH_BIN:-realpath}
vtool_bin=${VTOOL_BIN:-vtool}

fail() {
  echo "macOS deployment target verification failed: $*" >&2
  exit 1
}

require_tool() {
  local tool=$1
  command -v "$tool" >/dev/null 2>&1 || fail "required tool not found: $tool"
}

validate_version() {
  local version=$1
  [[ "$version" =~ ^[0-9]+(\.[0-9]+){0,2}$ ]]
}

version_is_greater() {
  local candidate=$1
  local limit=$2
  local candidate_major candidate_minor candidate_patch
  local limit_major limit_minor limit_patch

  IFS=. read -r candidate_major candidate_minor candidate_patch <<<"$candidate"
  IFS=. read -r limit_major limit_minor limit_patch <<<"$limit"
  candidate_minor=${candidate_minor:-0}
  candidate_patch=${candidate_patch:-0}
  limit_minor=${limit_minor:-0}
  limit_patch=${limit_patch:-0}

  if ((10#$candidate_major != 10#$limit_major)); then
    ((10#$candidate_major > 10#$limit_major))
    return
  fi
  if ((10#$candidate_minor != 10#$limit_minor)); then
    ((10#$candidate_minor > 10#$limit_minor))
    return
  fi
  ((10#$candidate_patch > 10#$limit_patch))
}

versions_equal() {
  local left=$1
  local right=$2
  ! version_is_greater "$left" "$right" && ! version_is_greater "$right" "$left"
}

extract_macos_minos() {
  awk '
    $1 == "cmd" {
      command = $2
      if (command == "LC_BUILD_VERSION") {
        build_platform = ""
      }
      next
    }
    command == "LC_BUILD_VERSION" && $1 == "platform" {
      build_platform = $2
      next
    }
    command == "LC_BUILD_VERSION" && $1 == "minos" {
      if (build_platform != "MACOS" && build_platform != "1") {
        invalid_platform = 1
      }
      count++
      value = $2
      next
    }
    command == "LC_VERSION_MIN_MACOSX" && $1 == "version" {
      count++
      value = $2
      next
    }
    END {
      if (invalid_platform || count != 1 || value == "") {
        exit 1
      }
      print value
    }
  '
}

validate_version "$supported_version" || fail "invalid supported version: $supported_version"
[[ -d "$app_bundle/Contents" ]] || fail "app bundle Contents directory not found: $app_bundle"

require_tool "$file_bin"
require_tool "$find_bin"
require_tool "$lipo_bin"
require_tool "$plistbuddy_bin"
require_tool "$realpath_bin"
require_tool "$vtool_bin"

app_bundle=$("$realpath_bin" "$app_bundle") || fail "cannot resolve app bundle path: $app_bundle"

info_plist="$app_bundle/Contents/Info.plist"
[[ -f "$info_plist" ]] || fail "Info.plist not found: $info_plist"

bundle_minimum=$(
  "$plistbuddy_bin" -c 'Print :LSMinimumSystemVersion' "$info_plist" 2>/dev/null
) || fail "cannot read LSMinimumSystemVersion from $info_plist"
validate_version "$bundle_minimum" || fail "invalid LSMinimumSystemVersion in $info_plist: $bundle_minimum"
versions_equal "$bundle_minimum" "$supported_version" || \
  fail "LSMinimumSystemVersion is ${bundle_minimum}，expected $supported_version"

executable_name=$(
  "$plistbuddy_bin" -c 'Print :CFBundleExecutable' "$info_plist" 2>/dev/null
) || fail "cannot read CFBundleExecutable from $info_plist"
[[ -n "$executable_name" ]] || fail "CFBundleExecutable is empty in $info_plist"
main_executable="$app_bundle/Contents/MacOS/$executable_name"
[[ -f "$main_executable" ]] || fail "main executable not found: $main_executable"

macho_count=0
main_seen=0
manifest=$(mktemp "${TMPDIR:-/tmp}/karte-macho-manifest.XXXXXX") || fail "cannot create scan manifest"
trap 'rm -f -- "$manifest"' EXIT

if ! "$find_bin" "$app_bundle/Contents" \( -type f -o -type l \) -print0 >"$manifest"; then
  fail "cannot enumerate every file and symlink under $app_bundle/Contents"
fi

while IFS= read -r -d '' binary; do
  if [[ -L "$binary" ]]; then
    resolved=$("$realpath_bin" "$binary") || fail "broken or unresolvable symlink: $binary"
    case "$resolved" in
      "$app_bundle/Contents"/*) ;;
      *) fail "bundle symlink escapes Contents: $binary -> $resolved" ;;
    esac
    continue
  fi

  file_output=$("$file_bin" -b "$binary") || fail "file could not inspect: $binary"
  case "$file_output" in
    *Mach-O*) ;;
    *) continue ;;
  esac

  macho_count=$((macho_count + 1))
  if [[ "$binary" == "$main_executable" ]]; then
    main_seen=1
  fi

  arches=$("$lipo_bin" -archs "$binary") || fail "lipo could not read architectures: $binary"
  [[ -n "$arches" ]] || fail "lipo returned no architectures: $binary"

  arch_count=0
  for arch in $arches; do
    [[ "$arch" =~ ^[A-Za-z0-9_]+$ ]] || fail "invalid architecture '$arch' reported for $binary"
    arch_count=$((arch_count + 1))

    build_output=$("$vtool_bin" -arch "$arch" -show-build "$binary") || \
      fail "vtool could not read load commands for $binary ($arch)"
    minos=$(printf '%s\n' "$build_output" | extract_macos_minos) || \
      fail "missing，ambiguous，or non-macOS deployment target in $binary ($arch)"
    validate_version "$minos" || fail "invalid deployment target '$minos' in $binary ($arch)"
    if version_is_greater "$minos" "$supported_version"; then
      fail "$binary ($arch) requires macOS ${minos}，newer than supported $supported_version"
    fi
    if ! versions_equal "$minos" "$supported_version"; then
      fail "$binary ($arch) targets macOS ${minos}，expected exactly $supported_version"
    fi

    relative_path=${binary#"$app_bundle"/}
    printf '%s\t%s\t%s\n' "$relative_path" "$arch" "$minos"
  done

  ((arch_count > 0)) || fail "no Mach-O slices found in $binary"
done <"$manifest"

((macho_count > 0)) || fail "no Mach-O files found in $app_bundle/Contents"
((main_seen == 1)) || fail "CFBundleExecutable is not a parseable Mach-O file: $main_executable"

echo "Verified $macho_count Mach-O files for macOS $supported_version support in $app_bundle"
