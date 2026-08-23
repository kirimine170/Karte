#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/lib/macos-toolchain.sh
source "$script_dir/lib/macos-toolchain.sh"

native_root=
target=
project_dir=$PWD

while (($# > 0)); do
  case "$1" in
    --native-root)
      [[ $# -ge 2 ]] || {
        echo "--native-root requires a path" >&2
        exit 2
      }
      native_root=$2
      shift 2
      ;;
    --target)
      [[ $# -ge 2 ]] || {
        echo "--target requires a value" >&2
        exit 2
      }
      target=$2
      shift 2
      ;;
    --project-dir)
      [[ $# -ge 2 ]] || {
        echo "--project-dir requires a path" >&2
        exit 2
      }
      project_dir=$2
      shift 2
      ;;
    --)
      shift
      break
      ;;
    -*|"")
      echo "Unknown macOS Wails option: $1" >&2
      exit 2
      ;;
    *)
      break
      ;;
  esac
done

action=${1:-}
[[ -n "$action" ]] || {
  echo "usage: macos-wails.sh --native-root PATH --target TARGET [--project-dir PATH] ACTION [ARGS...]" >&2
  exit 2
}
shift

[[ -n "$native_root" && -n "$target" ]] || {
  echo "--native-root and --target are required" >&2
  exit 2
}
[[ -d "$project_dir" && ! -L "$project_dir" ]] || {
  echo "macOS Wails project directory is missing，not a directory，or a symlink: $project_dir" >&2
  exit 1
}

case "$target" in
  darwin-arm64) architecture=arm64 ;;
  darwin-amd64) architecture=x86_64 ;;
  *)
    echo "Unsupported native macOS Wails target: $target" >&2
    exit 2
    ;;
esac

karte_load_macos_toolchain

go_bin=${GO_BIN:-go}
command -v "$go_bin" >/dev/null 2>&1 || {
  echo "Go command was not found: $go_bin" >&2
  exit 1
}
wails_bin=${KARTE_WAILS_BIN:-}
if [[ -z "$wails_bin" ]]; then
  wails_bin=$(command -v wails 2>/dev/null || true)
fi
[[ -n "$wails_bin" && -f "$wails_bin" && -x "$wails_bin" ]] || {
  echo "Wails CLI binary was not found or is not executable: $wails_bin" >&2
  exit 1
}
wails_bin=$(${REALPATH_BIN:-realpath} "$wails_bin")

module_version=$(cd "$project_dir" && "$go_bin" list -m -f '{{.Version}}' github.com/wailsapp/wails/v2) || {
  echo "Cannot resolve the Wails module version from $project_dir" >&2
  exit 1
}
[[ "$module_version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([+-][A-Za-z0-9.-]+)?$ ]] || {
  echo "Wails module version is not an immutable SemVer version: $module_version" >&2
  exit 1
}
binary_module_version=$(
  "$go_bin" version -m "$wails_bin" |
    awk '$1 == "mod" && $2 == "github.com/wailsapp/wails/v2" { count++; version=$3 } END { if (count == 1) print version }'
) || {
  echo "Cannot inspect Wails CLI Go binary metadata: $wails_bin" >&2
  exit 1
}
[[ -n "$binary_module_version" ]] || {
  echo "Wails CLI binary metadata does not contain exactly one Wails module identity: $wails_bin" >&2
  exit 1
}
[[ "$binary_module_version" == "$module_version" ]] || {
  echo "Wails CLI $binary_module_version does not match go.mod module $module_version" >&2
  exit 1
}

deployment_target=11.0
expected_stamp=$(karte_expected_native_stamp "$architecture" "$deployment_target")
stamp_path="$native_root/native-runtime.version"
[[ -f "$stamp_path" && ! -L "$stamp_path" ]] || {
  echo "Pinned native runtime stamp is missing or not regular: $stamp_path" >&2
  exit 1
}
actual_stamp=$(<"$stamp_path")
[[ "$actual_stamp" == "$expected_stamp" ]] || {
  echo "Pinned native runtime stamp does not match the selected Xcode/SDK/clang toolchain: $stamp_path" >&2
  exit 1
}

sherpa_lib_dir="$native_root/sherpa/lib"
portaudio_lib_dir="$native_root/portaudio/lib"
native_root_real=$(${REALPATH_BIN:-realpath} "$native_root") || {
  echo "Cannot resolve pinned native runtime root: $native_root" >&2
  exit 1
}
for library in \
  "$sherpa_lib_dir/libonnxruntime.dylib" \
  "$sherpa_lib_dir/libsherpa-onnx-c-api.dylib" \
  "$portaudio_lib_dir/libportaudio.dylib"; do
  [[ -f "$library" ]] || {
    echo "Pinned native runtime library is missing: $library" >&2
    exit 1
  }
  library_real=$(${REALPATH_BIN:-realpath} "$library") || {
    echo "Cannot resolve pinned native runtime library: $library" >&2
    exit 1
  }
  case "$library_real" in
    "$native_root_real"/*) ;;
    *)
      echo "Pinned native runtime library escapes its root: $library -> $library_real" >&2
      exit 1
      ;;
  esac
done
[[ "$sherpa_lib_dir" != *:* && "$portaudio_lib_dir" != *:* ]] || {
  echo "Native library paths containing ':' cannot be represented in DYLD_LIBRARY_PATH" >&2
  exit 1
}

quote_go_flag() {
  local value=$1
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  printf '"%s"' "$value"
}

sherpa_link_flag=$(quote_go_flag "-L$sherpa_lib_dir")
portaudio_link_flag=$(quote_go_flag "-L$portaudio_lib_dir")

export DEVELOPER_DIR=$KARTE_DEVELOPER_DIR_REAL
export MACOSX_DEPLOYMENT_TARGET=$deployment_target
export CGO_CFLAGS="-mmacosx-version-min=$deployment_target"
export CGO_CXXFLAGS="-mmacosx-version-min=$deployment_target"
export CGO_LDFLAGS="$sherpa_link_flag $portaudio_link_flag -framework UniformTypeIdentifiers -mmacosx-version-min=$deployment_target"
export DYLD_LIBRARY_PATH="$sherpa_lib_dir:$portaudio_lib_dir"
export PKG_CONFIG_PATH="$portaudio_lib_dir/pkgconfig"
export KARTE_NATIVE_ROOT=$native_root
export KARTE_SHERPA_LIB_DIR=$sherpa_lib_dir
export KARTE_PORTAUDIO_LIB_DIR=$portaudio_lib_dir
export KARTE_WAILS_BIN=$wails_bin

case "$action" in
  generate)
    cd "$project_dir"
    exec "$wails_bin" generate module "$@"
    ;;
  buildmatrix)
    cd "$project_dir"
    exec "$go_bin" run ./cmd/buildmatrix "$@"
    ;;
  run)
    (($# > 0)) || {
      echo "macos-wails.sh run requires a command" >&2
      exit 2
    }
    cd "$project_dir"
    exec "$@"
    ;;
  manifest)
    [[ $# == 1 ]] || {
      echo "macos-wails.sh manifest requires exactly one output path" >&2
      exit 2
    }
    native_stamp_sha256=$(printf '%s' "$actual_stamp" | ${SHASUM_BIN:-shasum} -a 256 | awk '{print $1}')
    export KARTE_MANIFEST_WAILS_VERSION=$module_version
    export KARTE_MANIFEST_WAILS_BIN=$wails_bin
    export KARTE_MANIFEST_NATIVE_TARGET=$target
    export KARTE_MANIFEST_NATIVE_ROOT=$native_root
    export KARTE_MANIFEST_NATIVE_STAMP_SHA256=$native_stamp_sha256
    export KARTE_MANIFEST_NATIVE_COLD=${KARTE_NATIVE_COLD:-0}
    exec "$script_dir/macos-toolchain-manifest.sh" --manifest "$1"
    ;;
  *)
    echo "Unknown macOS Wails action: $action" >&2
    exit 2
    ;;
esac
