#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/karte-macos-native-manifest.XXXXXX")
trap 'rm -rf -- "$fixture_root"' EXIT

app_bundle="$fixture_root/Karte.app"
frameworks="$app_bundle/Contents/Frameworks"
licenses="$app_bundle/Contents/Resources/THIRD_PARTY_LICENSES/native"
mkdir -p "$frameworks" "$licenses"

printf '%s\n' 'onnx bytes' >"$frameworks/libonnxruntime.1.27.0.dylib"
ln -s libonnxruntime.1.27.0.dylib "$frameworks/libonnxruntime.dylib"
printf '%s\n' 'sherpa bytes' >"$frameworks/libsherpa-onnx-c-api.dylib"
printf '%s\n' 'portaudio bytes' >"$frameworks/libportaudio.dylib"
printf '%s\n' 'MIT License' >"$licenses/onnxruntime-LICENSE"
printf '%s\n' 'Apache License Version 2.0' >"$licenses/sherpa-onnx-LICENSE"
printf '%s\n' 'MIT License' >"$licenses/portaudio-LICENSE.txt"

"$script_dir/capture-macos-native-compliance.sh" "$app_bundle" darwin-arm64

python3 - "$app_bundle/Contents/Resources/compliance/native-build.json" <<'PY'
import json
from pathlib import Path
import sys

manifest = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
assert manifest["schemaVersion"] == 1
assert manifest["platform"] == "darwin"
assert [record["componentId"] for record in manifest["packages"]] == [
    "native:onnxruntime-macos",
    "native:sherpa-onnx-macos",
    "native:portaudio-macos",
]
onnx_files = manifest["packages"][0]["files"]
assert len(onnx_files) == 2
link = next(item for item in onnx_files if item["artifactPath"].endswith("libonnxruntime.dylib"))
assert link["symlinkTarget"] == "libonnxruntime.1.27.0.dylib"
assert len(link["sha256"]) == 64
PY

ln -s "$fixture_root/outside" "$frameworks/libportaudio-escape.dylib"
if "$script_dir/capture-macos-native-compliance.sh" "$app_bundle" darwin-arm64 >/dev/null 2>&1; then
  echo 'Expected escaping native symlink capture to fail' >&2
  exit 1
fi

echo 'macOS native compliance manifest tests passed'
