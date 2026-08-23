#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
verifier="$script_dir/verify-macos-deployment-target.sh"
temp_dir=$(mktemp -d "${TMPDIR:-/tmp}/karte deployment target test.XXXXXX")
trap 'rm -rf -- "$temp_dir"' EXIT

tool_dir="$temp_dir/fake tools"
app_bundle="$temp_dir/Karte Fixture.app"
mkdir -p \
  "$tool_dir" \
  "$app_bundle/Contents/MacOS" \
  "$app_bundle/Contents/Frameworks/Nested.framework/Versions/A" \
  "$app_bundle/Contents/PlugIns/Preview Plugin.bundle/Contents/MacOS" \
  "$app_bundle/Contents/Resources"

printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' \
  '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
  '<plist version="1.0"><dict>' \
  '<key>CFBundleExecutable</key><string>Karte Fixture</string>' \
  '<key>LSMinimumSystemVersion</key><string>11.0.0</string>' \
  '</dict></plist>' >"$app_bundle/Contents/Info.plist"

touch \
  "$app_bundle/Contents/MacOS/Karte Fixture" \
  "$app_bundle/Contents/Frameworks/libaudio.dylib" \
  "$app_bundle/Contents/Frameworks/Nested.framework/Versions/A/Nested" \
  "$app_bundle/Contents/PlugIns/Preview Plugin.bundle/Contents/MacOS/Preview Plugin" \
  "$app_bundle/Contents/Resources/not-a-binary.txt"

cat >"$tool_dir/file" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
binary=${@: -1}
case "$binary" in
  *not-a-binary.txt|*Info.plist) echo 'ASCII text' ;;
  *) echo 'Mach-O fixture' ;;
esac
EOF

cat >"$tool_dir/find" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${FAIL_FIND:-0} == 1 ]]; then
  printf '%s\0' "$1/MacOS/Karte Fixture"
  exit 42
fi
exec /usr/bin/find "$@"
EOF

cat >"$tool_dir/lipo" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
binary=${@: -1}
case "$binary" in
  *Nested) echo 'x86_64 arm64' ;;
  *) echo 'arm64' ;;
esac
EOF

cat >"$tool_dir/vtool" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
arch=''
binary=${@: -1}
while (($#)); do
  if [[ $1 == -arch ]]; then
    arch=$2
    shift 2
    continue
  fi
  shift
done

if [[ -n ${FAIL_VTOOL_FOR:-} && $binary == *"$FAIL_VTOOL_FOR"* ]]; then
  echo 'fixture parse failure' >&2
  exit 1
fi

case "$binary:$arch" in
  *libaudio.dylib:arm64)
    minos=${LEGACY_DYLIB_MINOS:-11.0}
    printf '%s\n' 'Load command 1' '      cmd LC_VERSION_MIN_MACOSX' '  cmdsize 16' "  version $minos" '      sdk 11.0'
    ;;
  *Nested:arm64)
    minos=${ARM64_NESTED_MINOS:-11.0}
    printf '%s\n' 'Load command 1' '      cmd LC_BUILD_VERSION' '  cmdsize 32' ' platform MACOS' "    minos $minos" '      sdk 15.0'
    ;;
  *Nested:x86_64)
    printf '%s\n' 'Load command 1' '      cmd LC_BUILD_VERSION' '  cmdsize 32' ' platform 1' '    minos 11' '      sdk 15.0'
    ;;
  *Preview\ Plugin*:arm64)
    if [[ ${UNPARSEABLE_PLUGIN:-0} == 1 ]]; then
      printf '%s\n' 'Load command 1' '      cmd LC_UUID'
    else
      printf '%s\n' 'Load command 1' '      cmd LC_BUILD_VERSION' '  cmdsize 32' ' platform MACOS' '    minos 11.0.0' '      sdk 15.0'
    fi
    ;;
  *Karte\ Fixture:arm64)
    minos=${MAIN_MINOS:-11.0}
    printf '%s\n' 'Load command 1' '      cmd LC_BUILD_VERSION' '  cmdsize 32' ' platform MACOS' "    minos $minos" '      sdk 15.0'
    ;;
  *)
    printf '%s\n' 'Load command 1' '      cmd LC_BUILD_VERSION' '  cmdsize 32' ' platform MACOS' '    minos 11.0' '      sdk 15.0'
    ;;
esac
EOF

chmod +x "$tool_dir/file" "$tool_dir/find" "$tool_dir/lipo" "$tool_dir/vtool"
ln -s 'Versions/A/Nested' "$app_bundle/Contents/Frameworks/Nested.framework/Nested"

run_verifier() {
  FILE_BIN="$tool_dir/file" \
    FIND_BIN="$tool_dir/find" \
    LIPO_BIN="$tool_dir/lipo" \
    VTOOL_BIN="$tool_dir/vtool" \
    "$verifier" "$app_bundle" 11.0
}

expect_failure() {
  local description=$1
  shift
  if "$@" >"$temp_dir/failure.stdout" 2>"$temp_dir/failure.stderr"; then
    echo "expected failure: $description" >&2
    cat "$temp_dir/failure.stdout" >&2
    cat "$temp_dir/failure.stderr" >&2
    exit 1
  fi
}

valid_output=$(run_verifier)
grep -F $'Contents/MacOS/Karte Fixture\tarm64\t11.0' <<<"$valid_output" >/dev/null
grep -F $'Contents/Frameworks/libaudio.dylib\tarm64\t11.0' <<<"$valid_output" >/dev/null
grep -F $'Contents/Frameworks/Nested.framework/Versions/A/Nested\tx86_64\t11' <<<"$valid_output" >/dev/null
grep -F $'Contents/Frameworks/Nested.framework/Versions/A/Nested\tarm64\t11.0' <<<"$valid_output" >/dev/null
grep -F $'Contents/PlugIns/Preview Plugin.bundle/Contents/MacOS/Preview Plugin\tarm64\t11.0.0' <<<"$valid_output" >/dev/null
if grep -F 'not-a-binary.txt' <<<"$valid_output" >/dev/null; then
  echo 'non-Mach-O resource was not skipped' >&2
  exit 1
fi

expect_failure 'newer universal slice' env \
  FILE_BIN="$tool_dir/file" FIND_BIN="$tool_dir/find" LIPO_BIN="$tool_dir/lipo" VTOOL_BIN="$tool_dir/vtool" \
  ARM64_NESTED_MINOS=15.5 "$verifier" "$app_bundle" 11.0

expect_failure 'main executable target below policy' env \
  FILE_BIN="$tool_dir/file" FIND_BIN="$tool_dir/find" LIPO_BIN="$tool_dir/lipo" VTOOL_BIN="$tool_dir/vtool" \
  MAIN_MINOS=10.13 "$verifier" "$app_bundle" 11.0

expect_failure 'dylib target below policy' env \
  FILE_BIN="$tool_dir/file" FIND_BIN="$tool_dir/find" LIPO_BIN="$tool_dir/lipo" VTOOL_BIN="$tool_dir/vtool" \
  LEGACY_DYLIB_MINOS=10.15 "$verifier" "$app_bundle" 11.0

expect_failure 'unparseable load commands' env \
  FILE_BIN="$tool_dir/file" FIND_BIN="$tool_dir/find" LIPO_BIN="$tool_dir/lipo" VTOOL_BIN="$tool_dir/vtool" \
  UNPARSEABLE_PLUGIN=1 "$verifier" "$app_bundle" 11.0

expect_failure 'missing vtool' env \
  FILE_BIN="$tool_dir/file" FIND_BIN="$tool_dir/find" LIPO_BIN="$tool_dir/lipo" VTOOL_BIN="$tool_dir/does-not-exist" \
  "$verifier" "$app_bundle" 11.0

expect_failure 'partial find output followed by failure' env \
  FILE_BIN="$tool_dir/file" FIND_BIN="$tool_dir/find" LIPO_BIN="$tool_dir/lipo" VTOOL_BIN="$tool_dir/vtool" \
  FAIL_FIND=1 "$verifier" "$app_bundle" 11.0

ln -s "$temp_dir/outside.dylib" "$app_bundle/Contents/Frameworks/outside.dylib"
expect_failure 'symlink escaping bundle Contents' run_verifier
unlink "$app_bundle/Contents/Frameworks/outside.dylib"

wrong_plist="$temp_dir/wrong Info.plist"
cp "$app_bundle/Contents/Info.plist" "$wrong_plist"
sed 's/11\.0\.0/10.13.0/' "$wrong_plist" >"$app_bundle/Contents/Info.plist"
expect_failure 'Info.plist support floor mismatch' run_verifier
mv "$wrong_plist" "$app_bundle/Contents/Info.plist"

echo 'macOS deployment target verifier tests passed'
