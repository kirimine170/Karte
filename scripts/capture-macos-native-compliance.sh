#!/usr/bin/env bash

set -euo pipefail

app_bundle=${1:?usage: capture-macos-native-compliance.sh APP_BUNDLE TARGET}
target=${2:?usage: capture-macos-native-compliance.sh APP_BUNDLE TARGET}
python_bin=${PYTHON_BIN:-python3}

case "$target" in
  darwin-arm64|darwin-amd64) ;;
  *)
    echo "Unsupported macOS target: $target" >&2
    exit 1
    ;;
esac

"$python_bin" - "$app_bundle" "$target" <<'PY'
import hashlib
import json
import os
from pathlib import Path
import sys

root = Path(sys.argv[1]).resolve(strict=True)
target = sys.argv[2]
frameworks = root / "Contents" / "Frameworks"
licenses = root / "Contents" / "Resources" / "THIRD_PARTY_LICENSES" / "native"
output = root / "Contents" / "Resources" / "compliance" / "native-build.json"

definitions = [
    {
        "componentId": "native:onnxruntime-macos",
        "packageManager": "source-build",
        "packageName": "onnxruntime",
        "packageVersion": "1.27.0",
        "packageSource": "https://github.com/microsoft/onnxruntime/tree/8f0278c77bf44b0cc83c098c6c722b92a36ac4b5",
        "license": "onnxruntime-LICENSE",
        "glob": "libonnxruntime*.dylib",
    },
    {
        "componentId": "native:sherpa-onnx-macos",
        "packageManager": "source-build",
        "packageName": "sherpa-onnx",
        "packageVersion": "1.13.4",
        "packageSource": "https://github.com/k2-fsa/sherpa-onnx/tree/142807252687d81b40d6315f23470a1512a00de3",
        "license": "sherpa-onnx-LICENSE",
        "glob": "libsherpa-onnx*.dylib",
    },
    {
        "componentId": "native:portaudio-macos",
        "packageManager": "source-archive",
        "packageName": "portaudio",
        "packageVersion": "19.7.0",
        "packageSource": "https://files.portaudio.com/archives/pa_stable_v190700_20210406.tgz",
        "packageSourceSha256": "47efbf42c77c19a05d22e627d42873e991ec0c1357219c0d74ce6a2948cb2def",
        "license": "portaudio-LICENSE.txt",
        "glob": "libportaudio*.dylib",
    },
]

def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()

def confined(path: Path) -> Path:
    resolved = path.resolve(strict=True)
    if resolved != root and root not in resolved.parents:
        raise SystemExit(f"native path escapes app bundle: {path} -> {resolved}")
    return resolved

records = []
for definition in definitions:
    license_path = licenses / definition["license"]
    if license_path.is_symlink() or not license_path.is_file():
        raise SystemExit(f"native license is missing or symlinked: {license_path}")
    files = []
    matches = sorted(frameworks.glob(definition["glob"]), key=lambda item: item.name)
    if not matches:
        raise SystemExit(f"no native library matches {definition['glob']}")
    for path in matches:
        final = confined(path)
        relative = path.relative_to(root).as_posix()
        entry = {
            "artifactPath": relative,
            "bytes": final.stat().st_size,
            "sha256": sha256(final),
        }
        if path.is_symlink():
            entry["symlinkTarget"] = os.readlink(path)
        files.append(entry)
    record = {
        key: definition[key]
        for key in ("componentId", "packageManager", "packageName", "packageVersion", "packageSource")
    }
    if "packageSourceSha256" in definition:
        record["packageSourceSha256"] = definition["packageSourceSha256"]
    record.update({
        "licensePath": license_path.relative_to(root).as_posix(),
        "licenseSha256": sha256(license_path),
        "files": files,
        "properties": {"target": target},
    })
    records.append(record)

output.parent.mkdir(parents=True, exist_ok=True)
manifest = {"schemaVersion": 1, "platform": "darwin", "packages": records}
output.write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
print(f"Captured exact macOS native library，source，checksum，symlink，and license metadata at {output}")
PY
