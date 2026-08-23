#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/lib/macos-toolchain.sh
source "$script_dir/lib/macos-toolchain.sh"

manifest_path=
github_output=
while (($# > 0)); do
  case "$1" in
    --manifest)
      [[ $# -ge 2 ]] || {
        echo "--manifest requires a path" >&2
        exit 2
      }
      manifest_path=$2
      shift 2
      ;;
    --github-output)
      [[ $# -ge 2 ]] || {
        echo "--github-output requires a path" >&2
        exit 2
      }
      github_output=$2
      shift 2
      ;;
    *)
      echo "Unknown macOS toolchain manifest argument: $1" >&2
      exit 2
      ;;
  esac
done

[[ -n "$manifest_path" ]] || {
  echo "usage: macos-toolchain-manifest.sh --manifest PATH [--github-output PATH]" >&2
  exit 2
}

karte_load_macos_toolchain

python_bin=${PYTHON_BIN:-python3}
sw_vers_bin=${SW_VERS_BIN:-sw_vers}
command -v "$python_bin" >/dev/null 2>&1 || {
  echo "Python is required to write the macOS toolchain manifest" >&2
  exit 1
}
command -v "$sw_vers_bin" >/dev/null 2>&1 || {
  echo "sw_vers is required to identify the macOS build host" >&2
  exit 1
}
KARTE_HOST_OS_VERSION=$("$sw_vers_bin" -productVersion)
KARTE_HOST_OS_BUILD=$("$sw_vers_bin" -buildVersion)
KARTE_HOST_ARCH=$(uname -m)
karte_require_identifier host-os-version "$KARTE_HOST_OS_VERSION"
karte_require_identifier host-os-build "$KARTE_HOST_OS_BUILD"
karte_require_identifier host-architecture "$KARTE_HOST_ARCH"
export KARTE_HOST_OS_VERSION KARTE_HOST_OS_BUILD KARTE_HOST_ARCH

manifest_dir=$(dirname "$manifest_path")
mkdir -p "$manifest_dir"
temporary=$(mktemp "$manifest_dir/.macos-toolchain.XXXXXX")
trap 'rm -f -- "$temporary"' EXIT

export KARTE_MANIFEST_PATH=$temporary
"$python_bin" - <<'PY'
import json
import os

def optional(name):
    value = os.environ.get(name, "")
    return value or None

payload = {
    "schemaVersion": 1,
    "host": {
        "osVersion": os.environ["KARTE_HOST_OS_VERSION"],
        "osBuild": os.environ["KARTE_HOST_OS_BUILD"],
        "architecture": os.environ["KARTE_HOST_ARCH"],
        "runnerImage": optional("ImageOS"),
        "runnerImageVersion": optional("ImageVersion"),
    },
    "developerDir": os.environ["KARTE_DEVELOPER_DIR_REAL"],
    "xcode": {
        "version": os.environ["KARTE_XCODE_VERSION"],
        "build": os.environ["KARTE_XCODE_BUILD"],
    },
    "sdk": {
        "version": os.environ["KARTE_SDK_VERSION"],
        "build": os.environ["KARTE_SDK_BUILD"],
        "path": os.environ["KARTE_SDK_PATH"],
    },
    "clang": {
        "path": os.environ["KARTE_CLANG_PATH"],
        "identity": os.environ["KARTE_CLANG_IDENTITY"],
        "identitySha256": os.environ["KARTE_CLANG_IDENTITY_SHA256"],
    },
    "toolchainSha256": os.environ["KARTE_TOOLCHAIN_SHA256"],
    "cacheKey": os.environ["KARTE_TOOLCHAIN_CACHE_KEY"],
    "wails": {
        "moduleVersion": optional("KARTE_MANIFEST_WAILS_VERSION"),
        "binary": optional("KARTE_MANIFEST_WAILS_BIN"),
    },
    "native": {
        "target": optional("KARTE_MANIFEST_NATIVE_TARGET"),
        "root": optional("KARTE_MANIFEST_NATIVE_ROOT"),
        "stampSha256": optional("KARTE_MANIFEST_NATIVE_STAMP_SHA256"),
        "coldBuild": optional("KARTE_MANIFEST_NATIVE_COLD") == "1",
    },
}

with open(os.environ["KARTE_MANIFEST_PATH"], "w", encoding="utf-8") as output:
    json.dump(payload, output, ensure_ascii=False, indent=2, sort_keys=True)
    output.write("\n")
PY

chmod 0644 "$temporary"
mv -f "$temporary" "$manifest_path"
trap - EXIT

if [[ -n "$github_output" ]]; then
  printf 'cache_key=%s\n' "$KARTE_TOOLCHAIN_CACHE_KEY" >>"$github_output"
fi

echo "Verified Xcode $KARTE_XCODE_VERSION ($KARTE_XCODE_BUILD)，macOS SDK $KARTE_SDK_VERSION ($KARTE_SDK_BUILD)"
echo "Wrote macOS toolchain manifest to $manifest_path"
