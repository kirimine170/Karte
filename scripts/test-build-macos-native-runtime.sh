#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/karte-native-cache-test.XXXXXX")
trap 'rm -rf -- "$fixture_root"' EXIT

runtime_root="$fixture_root/runtime with spaces"
tool_root="$fixture_root/tools"
developer_dir="$fixture_root/Xcode Fixture.app/Contents/Developer"
clang_path="$developer_dir/Toolchains/XcodeDefault.xctoolchain/usr/bin/clang"
sdk_path="$developer_dir/Platforms/MacOSX.platform/Developer/SDKs/MacOSX99.4.sdk"
mkdir -p \
  "$runtime_root/sherpa/lib" \
  "$runtime_root/portaudio/lib" \
  "$runtime_root/licenses" \
  "$tool_root" \
  "$(dirname "$clang_path")" \
  "$sdk_path"

printf '%s\n' 'fixture ONNX Runtime license' >"$runtime_root/licenses/onnxruntime-LICENSE"
printf '%s\n' 'fixture sherpa-onnx license' >"$runtime_root/licenses/sherpa-onnx-LICENSE"
printf '%s\n' 'fixture PortAudio license' >"$runtime_root/licenses/portaudio-LICENSE.txt"
onnxruntime_license_sha256=$(shasum -a 256 "$runtime_root/licenses/onnxruntime-LICENSE" | awk '{print $1}')
sherpa_license_sha256=$(shasum -a 256 "$runtime_root/licenses/sherpa-onnx-LICENSE" | awk '{print $1}')
portaudio_license_sha256=$(shasum -a 256 "$runtime_root/licenses/portaudio-LICENSE.txt" | awk '{print $1}')

cat >"$tool_root/file" <<'EOF'
#!/usr/bin/env bash
case "${2:-}" in
  *.dylib) echo 'Mach-O 64-bit dynamically linked shared library arm64' ;;
  *) echo 'ASCII text' ;;
esac
EOF

cat >"$tool_root/find" <<'EOF'
#!/usr/bin/env bash
if [[ ${FAIL_NATIVE_FIND:-0} == 1 ]]; then
  printf '%s\0' "$1/sherpa/lib/libonnxruntime.dylib"
  exit 42
fi
exec /usr/bin/find "$@"
EOF

cat >"$tool_root/lipo" <<'EOF'
#!/usr/bin/env bash
awk -F= '$1 == "arch" { print $2 }' "$2"
EOF

cat >"$tool_root/python" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF

cat >"$tool_root/vtool" <<'EOF'
#!/usr/bin/env bash
minos=$(awk -F= '$1 == "minos" { print $2 }' "$4")
cat <<OUTPUT
Load command 1
      cmd LC_BUILD_VERSION
  cmdsize 32
 platform MACOS
    minos $minos
      sdk 15.5
OUTPUT
EOF

cat >"$tool_root/xcodebuild" <<'EOF'
#!/usr/bin/env bash
[[ ${1:-} == -version ]] || exit 2
printf '%s\n' 'Xcode 99.4' 'Build version 99Z123'
EOF

cat >"$tool_root/xcrun" <<EOF
#!/usr/bin/env bash
case "\$*" in
  '--sdk macosx --show-sdk-version') printf '%s\n' '99.4' ;;
  '--sdk macosx --show-sdk-build-version') printf '%s\n' '99ZSDK1' ;;
  '--sdk macosx --show-sdk-path') printf '%s\n' '$sdk_path' ;;
  '--sdk macosx --find clang') printf '%s\n' '$clang_path' ;;
  *) exit 2 ;;
esac
EOF

cat >"$clang_path" <<'EOF'
#!/usr/bin/env bash
[[ ${1:-} == --version ]] || exit 2
printf '%s\n' \
  'Apple clang version 99.4.0 (clang-fixture)' \
  'Target: arm64-apple-darwin99.4.0'
EOF

chmod +x \
  "$tool_root/file" \
  "$tool_root/find" \
  "$tool_root/lipo" \
  "$tool_root/python" \
  "$tool_root/vtool" \
  "$tool_root/xcodebuild" \
  "$tool_root/xcrun" \
  "$clang_path"

fake_clang_identity=$("$clang_path" --version)
fake_clang_sha256=$(printf '%s' "$fake_clang_identity" | shasum -a 256 | awk '{print $1}')

write_library() {
  local path=$1
  local arch=$2
  local minos=$3
  printf 'arch=%s\nminos=%s\n' "$arch" "$minos" >"$path"
}

write_stamp() {
  local arch=${1:-arm64}
  cat >"$runtime_root/native-runtime.version" <<EOF
format=2
sherpa=1.13.4@142807252687d81b40d6315f23470a1512a00de3
onnxruntime=1.27.0@8f0278c77bf44b0cc83c098c6c722b92a36ac4b5
portaudio=19.7.0@47efbf42c77c19a05d22e627d42873e991ec0c1357219c0d74ce6a2948cb2def
provider=cpu
arch=$arch
minos=11.0
xcode_version=99.4
xcode_build=99Z123
sdk_version=99.4
sdk_build=99ZSDK1
clang_identity_sha256=$fake_clang_sha256
EOF
}

run_cached_build() {
  local target=${1:-darwin-arm64}
  DEVELOPER_DIR="$developer_dir" \
  KARTE_EXPECTED_XCODE_VERSION="${TEST_EXPECTED_XCODE_VERSION:-99.4}" \
  KARTE_EXPECTED_XCODE_BUILD=99Z123 \
  KARTE_EXPECTED_SDK_VERSION=99.4 \
  KARTE_EXPECTED_SDK_BUILD="${TEST_EXPECTED_SDK_BUILD:-99ZSDK1}" \
  KARTE_NATIVE_COLD="${TEST_NATIVE_COLD:-0}" \
  KARTE_NATIVE_WORK_DIR="${TEST_NATIVE_WORK_DIR:-}" \
  KARTE_TEST_DARWIN=1 \
  XCODEBUILD_BIN="$tool_root/xcodebuild" \
  XCRUN_BIN="$tool_root/xcrun" \
  FILE_BIN="$tool_root/file" \
  FIND_BIN="$tool_root/find" \
  LIPO_BIN="$tool_root/lipo" \
  ONNXRUNTIME_LICENSE_SHA256="$onnxruntime_license_sha256" \
  SHERPA_LICENSE_SHA256="$sherpa_license_sha256" \
  PORTAUDIO_LICENSE_SHA256="$portaudio_license_sha256" \
  PYTHON_BIN="$tool_root/python" \
  VTOOL_BIN="$tool_root/vtool" \
    "$script_dir/build-macos-native-runtime.sh" "$runtime_root" "$target"
}

expect_failure() {
  local description=$1
  shift
  local output
  if output=$("$@" 2>&1); then
    echo "Expected failure: $description" >&2
    printf '%s\n' "$output" >&2
    exit 1
  fi
}

ort="$runtime_root/sherpa/lib/libonnxruntime.dylib"
sherpa="$runtime_root/sherpa/lib/libsherpa-onnx-c-api.dylib"
portaudio="$runtime_root/portaudio/lib/libportaudio.dylib"

write_library "$ort" arm64 11.0
write_library "$sherpa" arm64 11.0.0
write_library "$portaudio" arm64 11
write_stamp

valid_output=$(run_cached_build)
grep -Fq 'Using cached macOS native runtime' <<<"$valid_output"

write_library "$ort" arm64 15.5
expect_failure 'cache with a deployment target newer than policy' run_cached_build
write_library "$ort" arm64 11.0

write_library "$sherpa" x86_64 11.0
expect_failure 'cache with a wrong architecture' run_cached_build
write_library "$sherpa" arm64 11.0

write_library "$portaudio" arm64 10.15
expect_failure 'cache with a deployment target lower than the exact policy' run_cached_build
write_library "$portaudio" arm64 11.0

TEST_EXPECTED_XCODE_VERSION=99.5
expect_failure 'cache selected with a different expected Xcode version' run_cached_build
unset TEST_EXPECTED_XCODE_VERSION

TEST_EXPECTED_SDK_BUILD=99ZSDK2
expect_failure 'cache selected with a different expected SDK build' run_cached_build
unset TEST_EXPECTED_SDK_BUILD

unsafe_cold_root="$fixture_root/karte-native-user-selected"
mkdir -p "$unsafe_cold_root"
printf '%s\n' preserve >"$unsafe_cold_root/must-survive"
TEST_NATIVE_COLD=1
TEST_NATIVE_WORK_DIR=$unsafe_cold_root
expect_failure 'cold build with caller-selected deletion target' run_cached_build
unset TEST_NATIVE_COLD TEST_NATIVE_WORK_DIR
[[ $(<"$unsafe_cold_root/must-survive") == preserve ]] || {
  echo 'unsafe cold-build override was modified' >&2
  exit 1
}

FAIL_NATIVE_FIND=1
export FAIL_NATIVE_FIND
expect_failure 'partial native cache scan followed by find failure' run_cached_build
unset FAIL_NATIVE_FIND

outside_library="$fixture_root/outside.dylib"
write_library "$outside_library" arm64 11.0
ln -s "$outside_library" "$runtime_root/sherpa/lib/escaping.dylib"
expect_failure 'native cache symlink escaping output root' run_cached_build
unlink "$runtime_root/sherpa/lib/escaping.dylib"

mv "$runtime_root/licenses/onnxruntime-LICENSE" "$runtime_root/licenses/onnxruntime-LICENSE.missing"
expect_failure 'cache missing a required native license' run_cached_build
mv "$runtime_root/licenses/onnxruntime-LICENSE.missing" "$runtime_root/licenses/onnxruntime-LICENSE"

rm -f -- "$ort"
expect_failure 'cache missing a required library' run_cached_build

write_library "$ort" x86_64 11.0
write_library "$sherpa" x86_64 11.0
write_library "$portaudio" x86_64 11.0
write_stamp x86_64
intel_output=$(run_cached_build darwin-amd64)
grep -Fq 'for x86_64，macOS 11.0' <<<"$intel_output"

# shellcheck source=scripts/lib/macos-native-source.sh
source "$script_dir/lib/macos-native-source.sh"
source_repo="$fixture_root/source integrity repo"
mkdir -p "$source_repo"
git init -q "$source_repo"
git -C "$source_repo" config user.name 'Karte Fixture'
git -C "$source_repo" config user.email 'fixture@example.invalid'
printf '%s\n' pinned >"$source_repo/tracked.txt"
git -C "$source_repo" add tracked.txt
git -C "$source_repo" commit -qm fixture
source_commit=$(git -C "$source_repo" rev-parse HEAD)
karte_assert_clean_git_checkout "$source_repo" "$source_commit"

printf '%s\n' dirty >"$source_repo/tracked.txt"
expect_failure 'dirty tracked pinned Git source' \
  karte_assert_clean_git_checkout "$source_repo" "$source_commit"
printf '%s\n' pinned >"$source_repo/tracked.txt"
printf '%s\n' untracked >"$source_repo/untracked.txt"
expect_failure 'untracked file in pinned Git source' \
  karte_assert_clean_git_checkout "$source_repo" "$source_commit"

archive_fixture="$fixture_root/portaudio-fixture.tgz"
archive_input="$fixture_root/archive input"
mkdir -p "$archive_input/portaudio"
printf '%s\n' verified >"$archive_input/portaudio/CMakeLists.txt"
tar -czf "$archive_fixture" -C "$archive_input" portaudio
stale_extraction="$fixture_root/stale extraction"
mkdir -p "$stale_extraction/portaudio"
printf '%s\n' stale >"$stale_extraction/portaudio/CMakeLists.txt"
expect_failure 'stale extracted PortAudio source tree' \
  karte_extract_archive_fresh "$archive_fixture" "$stale_extraction" portaudio
[[ $(<"$stale_extraction/portaudio/CMakeLists.txt") == stale ]] || {
  echo 'stale PortAudio source was unexpectedly overwritten' >&2
  exit 1
}
fresh_extraction="$fixture_root/fresh extraction"
mkdir -p "$fresh_extraction"
karte_extract_archive_fresh "$archive_fixture" "$fresh_extraction" portaudio
[[ $(<"$fresh_extraction/portaudio/CMakeLists.txt") == verified ]] || {
  echo 'fresh PortAudio extraction did not use the fixture archive' >&2
  exit 1
}

echo 'macOS native runtime cache verification tests passed'
