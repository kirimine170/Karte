#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/lib/macos-toolchain.sh
source "$script_dir/lib/macos-toolchain.sh"
# shellcheck source=scripts/lib/macos-native-source.sh
source "$script_dir/lib/macos-native-source.sh"

final_output_root=${1:?usage: build-macos-native-runtime.sh OUTPUT_ROOT TARGET}
target=${2:?usage: build-macos-native-runtime.sh OUTPUT_ROOT TARGET}
deployment_target=${MACOSX_DEPLOYMENT_TARGET:-11.0}
file_bin=${FILE_BIN:-file}
find_bin=${FIND_BIN:-find}
lipo_bin=${LIPO_BIN:-lipo}
realpath_bin=${REALPATH_BIN:-realpath}
vtool_bin=${VTOOL_BIN:-vtool}
cold_build=${KARTE_NATIVE_COLD:-0}
verify_only=${KARTE_NATIVE_VERIFY_ONLY:-0}

case "$target" in
  darwin-arm64) architecture=arm64 ;;
  darwin-amd64) architecture=x86_64 ;;
  *)
    echo "Unsupported macOS target: $target" >&2
    exit 1
    ;;
esac

if [[ "$deployment_target" != 11.0 ]]; then
  echo "Karte release native libraries must be built with MACOSX_DEPLOYMENT_TARGET=11.0，got $deployment_target" >&2
  exit 1
fi

[[ "$cold_build" == 0 || "$cold_build" == 1 ]] || {
  echo "KARTE_NATIVE_COLD must be 0 or 1，got $cold_build" >&2
  exit 1
}
[[ "$verify_only" == 0 || "$verify_only" == 1 ]] || {
  echo "KARTE_NATIVE_VERIFY_ONLY must be 0 or 1，got $verify_only" >&2
  exit 1
}
if [[ "$cold_build" == 1 && "$verify_only" == 1 ]]; then
  echo "KARTE_NATIVE_COLD and KARTE_NATIVE_VERIFY_ONLY cannot be combined" >&2
  exit 1
fi

karte_load_macos_toolchain

onnxruntime_commit=8f0278c77bf44b0cc83c098c6c722b92a36ac4b5
onnxruntime_version=1.27.0
sherpa_commit=142807252687d81b40d6315f23470a1512a00de3
sherpa_version=1.13.4
portaudio_version=19.7.0
portaudio_archive=pa_stable_v190700_20210406.tgz
portaudio_url="https://files.portaudio.com/archives/$portaudio_archive"
portaudio_sha256=47efbf42c77c19a05d22e627d42873e991ec0c1357219c0d74ce6a2948cb2def
onnxruntime_license_sha256=${ONNXRUNTIME_LICENSE_SHA256:-2f07c72751aed99790b8a4869cf2311df85a860b22ded05fa22803587a48922c}
sherpa_license_sha256=${SHERPA_LICENSE_SHA256:-cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30}
portaudio_license_sha256=${PORTAUDIO_LICENSE_SHA256:-ec52a1952d701f94e5135719a47376da4ee0b4a0201f1cafb49f61db6480ac3d}

python_bin=${PYTHON_BIN:-python3}
parallel_jobs=${KARTE_NATIVE_JOBS:-4}
requested_work_root=${KARTE_NATIVE_WORK_DIR:-}
work_root=
build_root=
ephemeral_work_root=0
output_root=$final_output_root
stamp="$final_output_root/native-runtime.version"
expected_stamp=$(karte_expected_native_stamp "$architecture" "$deployment_target")
native_manifest=
stage_root=
backup_root=

cleanup() {
  if [[ -n "$native_manifest" ]]; then
    rm -f -- "$native_manifest"
  fi
  if [[ -n "$stage_root" && -d "$stage_root" ]]; then
    rm -rf -- "$stage_root"
  fi
  if [[ -n "$backup_root" && -d "$backup_root" ]]; then
    rm -rf -- "$backup_root"
  fi
  if [[ -n "$build_root" && -d "$build_root" ]]; then
    rm -rf -- "$build_root"
  fi
  if [[ "$ephemeral_work_root" == 1 && -n "$work_root" && -d "$work_root" ]]; then
    rm -rf -- "$work_root"
  fi
}
trap cleanup EXIT

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Required native build tool not found: $1" >&2
    exit 1
  }
}

require_tool cmake
require_tool curl
require_tool git
require_tool make
require_tool awk
require_tool shasum
require_tool tar
require_tool "$python_bin"
require_tool "$file_bin"
require_tool "$find_bin"
require_tool "$lipo_bin"
require_tool "$realpath_bin"
require_tool "$vtool_bin"

[[ "$parallel_jobs" =~ ^[1-9][0-9]*$ ]] || {
  echo "KARTE_NATIVE_JOBS must be a positive integer，got $parallel_jobs" >&2
  exit 1
}

"$python_bin" -c 'import sys; raise SystemExit(0 if sys.version_info >= (3, 10) else 1)' || {
  echo "ONNX Runtime $onnxruntime_version requires Python 3.10 or newer" >&2
  exit 1
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

native_fail() {
  echo "macOS native runtime verification failed: $*" >&2
  exit 1
}

verify_native_output() {
  local expected_ort="$output_root/sherpa/lib/libonnxruntime.dylib"
  local expected_sherpa="$output_root/sherpa/lib/libsherpa-onnx-c-api.dylib"
  local expected_portaudio="$output_root/portaudio/lib/libportaudio.dylib"
  local runtime_root binary resolved file_output arches arch arch_count build_output minos
  local expected_ort_real expected_sherpa_real expected_portaudio_real binary_real
  local ort_seen=0 sherpa_seen=0 portaudio_seen=0 macho_count=0

  for binary in "$expected_ort" "$expected_sherpa" "$expected_portaudio"; do
    [[ -f "$binary" ]] || native_fail "expected library is missing: $binary"
  done

  verify_license_file "$output_root/licenses/onnxruntime-LICENSE" "$onnxruntime_license_sha256"
  verify_license_file "$output_root/licenses/sherpa-onnx-LICENSE" "$sherpa_license_sha256"
  verify_license_file "$output_root/licenses/portaudio-LICENSE.txt" "$portaudio_license_sha256"

  runtime_root=$("$realpath_bin" "$output_root") || native_fail "cannot resolve output root: $output_root"
  expected_ort_real=$("$realpath_bin" "$expected_ort") || native_fail "cannot resolve $expected_ort"
  expected_sherpa_real=$("$realpath_bin" "$expected_sherpa") || native_fail "cannot resolve $expected_sherpa"
  expected_portaudio_real=$("$realpath_bin" "$expected_portaudio") || native_fail "cannot resolve $expected_portaudio"

  native_manifest=$(mktemp "${TMPDIR:-/tmp}/karte-native-manifest.XXXXXX") || \
    native_fail "cannot create scan manifest"
  if ! "$find_bin" "$runtime_root" \( -type f -o -type l \) -print0 >"$native_manifest"; then
    native_fail "cannot enumerate every native runtime file and symlink under $runtime_root"
  fi

  while IFS= read -r -d '' binary; do
    if [[ -L "$binary" ]]; then
      resolved=$("$realpath_bin" "$binary") || native_fail "broken or unresolvable symlink: $binary"
      case "$resolved" in
        "$runtime_root"/*) ;;
        *) native_fail "native runtime symlink escapes output root: $binary -> $resolved" ;;
      esac
      continue
    fi

    file_output=$("$file_bin" -b "$binary") || native_fail "file could not inspect: $binary"
    case "$file_output" in
      *Mach-O*) ;;
      *) continue ;;
    esac

    macho_count=$((macho_count + 1))
    arches=$("$lipo_bin" -archs "$binary") || native_fail "lipo could not inspect: $binary"
    [[ -n "$arches" ]] || native_fail "lipo returned no architectures: $binary"

    arch_count=0
    for arch in $arches; do
      arch_count=$((arch_count + 1))
      [[ "$arch" == "$architecture" ]] || \
        native_fail "$binary has architecture ${arch}，expected only $architecture"
      build_output=$("$vtool_bin" -arch "$arch" -show-build "$binary") || \
        native_fail "vtool could not inspect $binary ($arch)"
      minos=$(printf '%s\n' "$build_output" | extract_macos_minos) || \
        native_fail "missing，ambiguous，or non-macOS deployment target in $binary ($arch)"
      [[ "$minos" =~ ^[0-9]+(\.[0-9]+){0,2}$ ]] || \
        native_fail "invalid deployment target '$minos' in $binary ($arch)"
      versions_equal "$minos" "$deployment_target" || \
        native_fail "$binary ($arch) targets macOS ${minos}，expected exactly $deployment_target"
    done
    ((arch_count == 1)) || native_fail "$binary must contain exactly one $architecture slice"

    binary_real=$("$realpath_bin" "$binary") || native_fail "cannot resolve Mach-O file: $binary"
    [[ "$binary_real" != "$expected_ort_real" ]] || ort_seen=1
    [[ "$binary_real" != "$expected_sherpa_real" ]] || sherpa_seen=1
    [[ "$binary_real" != "$expected_portaudio_real" ]] || portaudio_seen=1
  done <"$native_manifest"

  rm -f -- "$native_manifest"
  native_manifest=

  ((macho_count > 0)) || native_fail "no Mach-O files found under $runtime_root"
  ((ort_seen == 1)) || native_fail "libonnxruntime.dylib is not a parseable Mach-O file"
  ((sherpa_seen == 1)) || native_fail "libsherpa-onnx-c-api.dylib is not a parseable Mach-O file"
  ((portaudio_seen == 1)) || native_fail "libportaudio.dylib is not a parseable Mach-O file"
  echo "Verified $macho_count pinned native Mach-O files for ${architecture}，macOS $deployment_target"
}

verify_license_file() {
  local path=$1
  local expected_hash=$2
  local actual_hash
  [[ -f "$path" && ! -L "$path" ]] || native_fail "expected native license is missing or not regular: $path"
  actual_hash=$(shasum -a 256 "$path" | awk '{print $1}') || native_fail "cannot hash native license: $path"
  [[ "$actual_hash" == "$expected_hash" ]] || native_fail "native license SHA-256 mismatch: $path"
}

if [[ "$cold_build" == 0 && -f "$stamp" && ! -L "$stamp" ]] && [[ $(<"$stamp") == "$expected_stamp" ]]; then
  verify_native_output
  echo "Using cached macOS native runtime from $output_root"
  exit 0
fi

if [[ "$verify_only" == 1 ]]; then
  native_fail "cache stamp does not match the selected Xcode/SDK/clang toolchain"
fi

case "$final_output_root" in
  ""|/|.|..)
    native_fail "unsafe output root: $final_output_root"
    ;;
esac
[[ ! -L "$final_output_root" ]] || native_fail "output root must not be a symlink: $final_output_root"

output_parent=$(dirname "$final_output_root")
trusted_temp_input=${RUNNER_TEMP:-${TMPDIR:-/tmp}}
[[ -n "$trusted_temp_input" && "$trusted_temp_input" == /* ]] || \
  native_fail "RUNNER_TEMP or TMPDIR must resolve from an absolute path"
[[ -d "$trusted_temp_input" ]] || native_fail "trusted temporary directory is missing: $trusted_temp_input"
trusted_temp_root=$("$realpath_bin" "$trusted_temp_input") || \
  native_fail "cannot resolve trusted temporary directory: $trusted_temp_input"
[[ -d "$trusted_temp_root" && ! -L "$trusted_temp_root" ]] || \
  native_fail "resolved trusted temporary directory is invalid: $trusted_temp_root"

if [[ "$cold_build" == 1 ]]; then
  [[ -z "$requested_work_root" ]] || \
    native_fail "KARTE_NATIVE_WORK_DIR is forbidden in cold mode; a fresh trusted temporary root is mandatory"
  work_root=$(mktemp -d "$trusted_temp_root/karte-native-cold-$architecture.XXXXXX") || \
    native_fail "cannot create a fresh cold-build work directory under $trusted_temp_root"
  ephemeral_work_root=1
else
  work_root=${requested_work_root:-"$trusted_temp_root/karte-native-$architecture-$KARTE_TOOLCHAIN_SHA256"}
  [[ "$work_root" == /* ]] || native_fail "native work directory must be absolute: $work_root"
  case "$(basename "$work_root")" in
    karte-native-*) ;;
    *) native_fail "native work directory basename must start with karte-native-: $work_root" ;;
  esac
  [[ ! -L "$work_root" ]] || native_fail "native work directory must not be a symlink: $work_root"
  mkdir -p "$work_root"
fi

mkdir -p "$output_parent"
build_root=$(mktemp -d "$work_root/karte-native-build.XXXXXX") || \
  native_fail "cannot create a fresh native source/build session under $work_root"
stage_root=$(mktemp -d "$output_parent/.native-runtime-stage.XXXXXX") || \
  native_fail "cannot create native runtime staging directory under $output_parent"
output_root=$stage_root
stamp="$output_root/native-runtime.version"

checkout_commit() {
  local repository=$1
  local commit=$2
  local destination=$3

  [[ ! -e "$destination" && ! -L "$destination" ]] || {
    echo "Refusing to reuse a stale Git source tree: $destination" >&2
    exit 1
  }
  git init -q "$destination"
  git -C "$destination" remote add origin "$repository"
  git -C "$destination" fetch --depth 1 origin "$commit"
  git -C "$destination" checkout -q --detach FETCH_HEAD
  karte_assert_clean_git_checkout "$destination" "$commit"
}

portaudio_download="$work_root/$portaudio_archive"
if [[ -e "$portaudio_download" || -L "$portaudio_download" ]]; then
  [[ -f "$portaudio_download" && ! -L "$portaudio_download" ]] || \
    native_fail "PortAudio source cache is not a regular file: $portaudio_download"
fi
if [[ ! -f "$portaudio_download" ]]; then
  curl --fail --location --retry 3 --output "$portaudio_download" "$portaudio_url"
fi
actual_portaudio_sha=$(shasum -a 256 "$portaudio_download" | awk '{print $1}')
if [[ "$actual_portaudio_sha" != "$portaudio_sha256" ]]; then
  echo "PortAudio archive SHA-256 mismatch: $actual_portaudio_sha" >&2
  exit 1
fi

portaudio_source="$build_root/portaudio"
karte_extract_archive_fresh "$portaudio_download" "$build_root" portaudio
[[ -f "$portaudio_source/CMakeLists.txt" ]] || {
  echo "PortAudio $portaudio_version source was not extracted at $portaudio_source" >&2
  exit 1
}

portaudio_build="$build_root/portaudio-build"
cmake -S "$portaudio_source" -B "$portaudio_build" \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="$output_root/portaudio" \
  -DCMAKE_OSX_ARCHITECTURES="$architecture" \
  -DCMAKE_OSX_DEPLOYMENT_TARGET="$deployment_target" \
  -DCMAKE_POLICY_VERSION_MINIMUM=3.5 \
  -DPA_BUILD_EXAMPLES=OFF \
  -DPA_BUILD_SHARED=ON \
  -DPA_BUILD_STATIC=OFF \
  -DPA_BUILD_TESTS=OFF
cmake --build "$portaudio_build" --config Release --parallel "$parallel_jobs"
cmake --install "$portaudio_build" --config Release

onnxruntime_source="$build_root/onnxruntime"
checkout_commit https://github.com/microsoft/onnxruntime.git "$onnxruntime_commit" "$onnxruntime_source"
onnxruntime_build="$build_root/onnxruntime-build"

# Karte ships and defaults to the CPU provider. CoreML in ORT 1.27 uses APIs
# introduced after macOS 11，so it cannot satisfy this release floor.
"$python_bin" "$onnxruntime_source/tools/ci_build/build.py" \
  --config Release \
  --build_dir "$onnxruntime_build" \
  --build_shared_lib \
  --parallel "$parallel_jobs" \
  --skip_tests \
  --cmake_extra_defines \
    "CMAKE_IGNORE_PREFIX_PATH=/opt/homebrew;/usr/local" \
    "CMAKE_OSX_ARCHITECTURES=$architecture" \
    "CMAKE_OSX_DEPLOYMENT_TARGET=$deployment_target" \
    CMAKE_POLICY_VERSION_MINIMUM=3.5 \
    onnxruntime_BUILD_UNIT_TESTS=OFF \
    onnxruntime_USE_COREML=OFF

onnxruntime_lib_dir="$onnxruntime_build/Release"
[[ -f "$onnxruntime_lib_dir/libonnxruntime.dylib" ]] || {
  echo "ONNX Runtime build did not produce libonnxruntime.dylib in $onnxruntime_lib_dir" >&2
  exit 1
}

sherpa_source="$build_root/sherpa-onnx"
checkout_commit https://github.com/k2-fsa/sherpa-onnx.git "$sherpa_commit" "$sherpa_source"
sherpa_build="$build_root/sherpa-onnx-build"

SHERPA_ONNXRUNTIME_INCLUDE_DIR="$onnxruntime_source/include/onnxruntime/core/session" \
SHERPA_ONNXRUNTIME_LIB_DIR="$onnxruntime_lib_dir" \
cmake -S "$sherpa_source" -B "$sherpa_build" \
  -DBUILD_SHARED_LIBS=ON \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="$output_root/sherpa" \
  -DCMAKE_CXX_FLAGS=-DSHERPA_ONNX_DISABLE_COREML=1 \
  -DCMAKE_OSX_ARCHITECTURES="$architecture" \
  -DCMAKE_OSX_DEPLOYMENT_TARGET="$deployment_target" \
  -DCMAKE_POLICY_VERSION_MINIMUM=3.5 \
  -DSHERPA_ONNX_BUILD_C_API_EXAMPLES=OFF \
  -DSHERPA_ONNX_ENABLE_BINARY=OFF \
  -DSHERPA_ONNX_ENABLE_CHECK=OFF \
  -DSHERPA_ONNX_ENABLE_C_API=ON \
  -DSHERPA_ONNX_ENABLE_JNI=OFF \
  -DSHERPA_ONNX_ENABLE_PORTAUDIO=OFF \
  -DSHERPA_ONNX_ENABLE_PYTHON=OFF \
  -DSHERPA_ONNX_ENABLE_TESTS=OFF \
  -DSHERPA_ONNX_ENABLE_WEBSOCKET=OFF
cmake --build "$sherpa_build" --config Release --parallel "$parallel_jobs"
cmake --install "$sherpa_build" --config Release

[[ -f "$output_root/sherpa/lib/libonnxruntime.dylib" ]] || {
  echo "sherpa install did not include libonnxruntime.dylib" >&2
  exit 1
}
[[ -f "$output_root/sherpa/lib/libsherpa-onnx-c-api.dylib" ]] || {
  echo "sherpa install did not include libsherpa-onnx-c-api.dylib" >&2
  exit 1
}
[[ -f "$output_root/portaudio/lib/libportaudio.dylib" ]] || {
  echo "PortAudio install did not include libportaudio.dylib" >&2
  exit 1
}

mkdir -p "$output_root/licenses"
cp -p "$onnxruntime_source/LICENSE" "$output_root/licenses/onnxruntime-LICENSE"
cp -p "$sherpa_source/LICENSE" "$output_root/licenses/sherpa-onnx-LICENSE"
cp -p "$portaudio_source/LICENSE.txt" "$output_root/licenses/portaudio-LICENSE.txt"
chmod 0644 \
  "$output_root/licenses/onnxruntime-LICENSE" \
  "$output_root/licenses/sherpa-onnx-LICENSE" \
  "$output_root/licenses/portaudio-LICENSE.txt"

verify_native_output
printf '%s\n' "$expected_stamp" >"$stamp"

backup_root=$(mktemp -d "$output_parent/.native-runtime-backup.XXXXXX") || \
  native_fail "cannot reserve native runtime backup path under $output_parent"
rmdir "$backup_root"
if [[ -e "$final_output_root" ]]; then
  mv "$final_output_root" "$backup_root" || native_fail "cannot move previous native runtime to backup"
fi
if ! mv "$stage_root" "$final_output_root"; then
  if [[ -d "$backup_root" ]]; then
    mv "$backup_root" "$final_output_root" || true
  fi
  native_fail "cannot promote verified native runtime staging directory"
fi
stage_root=
if [[ -d "$backup_root" ]]; then
  rm -rf -- "$backup_root"
fi
backup_root=

echo "Built pinned macOS native runtime for $architecture at $final_output_root"
