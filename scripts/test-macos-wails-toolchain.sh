#!/usr/bin/env bash

set -euo pipefail

if [[ $(uname -s) != Darwin ]]; then
  echo 'macOS Wails toolchain integration test skipped on non-Darwin host'
  exit 0
fi

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
project_root=$(cd "$script_dir/.." && pwd)
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/karte macos toolchain test.XXXXXX")
trap 'rm -rf -- "$fixture_root"' EXIT

real_go=$(command -v go)
real_developer_dir=${DEVELOPER_DIR:-$(xcode-select -p)}
real_clang=$(DEVELOPER_DIR="$real_developer_dir" xcrun --sdk macosx --find clang)
real_sdk_path=$(DEVELOPER_DIR="$real_developer_dir" xcrun --sdk macosx --show-sdk-path)

expect_failure() {
  local description=$1
  shift
  local output
  if output=$("$@" 2>&1); then
    echo "Expected failure: $description" >&2
    printf '%s\n' "$output" >&2
    exit 1
  fi
  printf '%s\n' "$output"
}

mock_root="$fixture_root/mock tools with spaces"
mock_developer_dir="$fixture_root/Xcode Fixture.app/Contents/Developer"
mock_clang="$mock_developer_dir/Toolchains/XcodeDefault.xctoolchain/usr/bin/clang"
mock_sdk="$mock_developer_dir/Platforms/MacOSX.platform/Developer/SDKs/MacOSX99.4.sdk"
mock_project="$fixture_root/project with spaces"
mock_native="$fixture_root/native runtime with spaces"
mkdir -p \
  "$mock_root" \
  "$(dirname "$mock_clang")" \
  "$mock_sdk" \
  "$mock_project" \
  "$mock_native/sherpa/lib" \
  "$mock_native/portaudio/lib/pkgconfig"

cat >"$mock_root/xcodebuild" <<'EOF'
#!/usr/bin/env bash
[[ ${1:-} == -version ]] || exit 2
printf '%s\n' 'Xcode 99.4' 'Build version 99Z123'
EOF

cat >"$mock_root/xcrun" <<EOF
#!/usr/bin/env bash
case "\$*" in
  '--sdk macosx --show-sdk-version') printf '%s\n' '99.4' ;;
  '--sdk macosx --show-sdk-build-version') printf '%s\n' '99ZSDK1' ;;
  '--sdk macosx --show-sdk-path') printf '%s\n' '$mock_sdk' ;;
  '--sdk macosx --find clang') printf '%s\n' '$mock_clang' ;;
  *) exit 2 ;;
esac
EOF

cat >"$mock_clang" <<'EOF'
#!/usr/bin/env bash
[[ ${1:-} == --version ]] || exit 2
printf '%s\n' \
  'Apple clang version 99.4.0 (clang-fixture)' \
  'Target: arm64-apple-darwin99.4.0'
EOF

mock_record="$fixture_root/wails record.txt"
cat >"$fixture_root/mock-wails.go" <<'EOF'
package main

import (
	"fmt"
	"os"
)

func main() {
	record, err := os.Create(os.Getenv("KARTE_TEST_WAILS_RECORD"))
	if err != nil {
		panic(err)
	}
	defer record.Close()
	for _, name := range []string{"DYLD_LIBRARY_PATH", "CGO_CFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS", "KARTE_WAILS_BIN"} {
		fmt.Fprintf(record, "%s=<%s>\n", map[string]string{
			"DYLD_LIBRARY_PATH": "DYLD",
			"CGO_CFLAGS": "CFLAGS",
			"CGO_CXXFLAGS": "CXXFLAGS",
			"CGO_LDFLAGS": "LDFLAGS",
			"KARTE_WAILS_BIN": "WAILS",
		}[name], os.Getenv(name))
	}
	for _, argument := range os.Args[1:] {
		fmt.Fprintf(record, "ARG=<%s>\n", argument)
	}
}
EOF
"$real_go" build -o "$mock_root/wails fixture" "$fixture_root/mock-wails.go"

cat >"$mock_root/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${1:-} == list && ${2:-} == -m ]]; then
  printf '%s\n' 'v2.13.0'
  exit 0
fi
if [[ ${1:-} == version && ${2:-} == -m ]]; then
  version=${KARTE_TEST_WAILS_METADATA_VERSION:-v2.13.0}
  printf '%s\n' \
    "$3: go1.25.0" \
    $'\tpath\tgithub.com/wailsapp/wails/v2/cmd/wails' \
    $'\tmod\tgithub.com/wailsapp/wails/v2\t'"$version"$'\th1:fixture'
  exit 0
fi
echo "unexpected fake go invocation: $*" >&2
exit 2
EOF

chmod +x \
  "$mock_root/xcodebuild" \
  "$mock_root/xcrun" \
  "$mock_root/go" \
  "$mock_clang"

for library in \
  "$mock_native/sherpa/lib/libonnxruntime.dylib" \
  "$mock_native/sherpa/lib/libsherpa-onnx-c-api.dylib" \
  "$mock_native/portaudio/lib/libportaudio.dylib"; do
  printf '%s\n' fixture >"$library"
done

(
  export DEVELOPER_DIR=$mock_developer_dir
  export KARTE_EXPECTED_XCODE_VERSION=99.4
  export KARTE_EXPECTED_XCODE_BUILD=99Z123
  export KARTE_EXPECTED_SDK_VERSION=99.4
  export KARTE_EXPECTED_SDK_BUILD=99ZSDK1
  export KARTE_TEST_DARWIN=1
  export XCODEBUILD_BIN=$mock_root/xcodebuild
  export XCRUN_BIN=$mock_root/xcrun
  # shellcheck source=scripts/lib/macos-toolchain.sh
  source "$script_dir/lib/macos-toolchain.sh"
  karte_load_macos_toolchain
  karte_expected_native_stamp arm64 11.0 >"$mock_native/native-runtime.version"
)

run_mock_wrapper() {
  DEVELOPER_DIR="$mock_developer_dir" \
  KARTE_EXPECTED_XCODE_VERSION="${KARTE_TEST_EXPECTED_XCODE_VERSION:-99.4}" \
  KARTE_EXPECTED_XCODE_BUILD=99Z123 \
  KARTE_EXPECTED_SDK_VERSION=99.4 \
  KARTE_EXPECTED_SDK_BUILD="${KARTE_TEST_EXPECTED_SDK_BUILD:-99ZSDK1}" \
  KARTE_TEST_DARWIN=1 \
  XCODEBUILD_BIN="$mock_root/xcodebuild" \
  XCRUN_BIN="$mock_root/xcrun" \
  GO_BIN="$mock_root/go" \
  KARTE_WAILS_BIN="$mock_root/wails fixture" \
  KARTE_TEST_WAILS_RECORD="$mock_record" \
    "$script_dir/macos-wails.sh" \
      --native-root "$mock_native" \
      --target darwin-arm64 \
      --project-dir "$mock_project" \
      generate -tags 'tag with spaces'
}

run_mock_wrapper
mock_wails_real=$(realpath "$mock_root/wails fixture")
grep -Fxq "DYLD=<$mock_native/sherpa/lib:$mock_native/portaudio/lib>" "$mock_record"
grep -Fxq 'CFLAGS=<-mmacosx-version-min=11.0>' "$mock_record"
grep -Fxq 'CXXFLAGS=<-mmacosx-version-min=11.0>' "$mock_record"
grep -Fq "\"-L$mock_native/sherpa/lib\" \"-L$mock_native/portaudio/lib\" -framework UniformTypeIdentifiers -mmacosx-version-min=11.0" "$mock_record"
grep -Fxq "WAILS=<$mock_wails_real>" "$mock_record"
grep -Fxq 'ARG=<generate>' "$mock_record"
grep -Fxq 'ARG=<module>' "$mock_record"
grep -Fxq 'ARG=<tag with spaces>' "$mock_record"

mock_manifest="$fixture_root/mock toolchain manifest.json"
mock_github_output="$fixture_root/mock github output.txt"
DEVELOPER_DIR="$mock_developer_dir" \
KARTE_EXPECTED_XCODE_VERSION=99.4 \
KARTE_EXPECTED_XCODE_BUILD=99Z123 \
KARTE_EXPECTED_SDK_VERSION=99.4 \
KARTE_EXPECTED_SDK_BUILD=99ZSDK1 \
KARTE_TEST_DARWIN=1 \
XCODEBUILD_BIN="$mock_root/xcodebuild" \
XCRUN_BIN="$mock_root/xcrun" \
  "$script_dir/macos-toolchain-manifest.sh" \
    --manifest "$mock_manifest" \
    --github-output "$mock_github_output" >/dev/null
python3 - "$mock_manifest" "$mock_developer_dir" <<'PY'
import json
import pathlib
import sys

manifest = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
assert manifest["schemaVersion"] == 1
assert manifest["host"]["osVersion"]
assert manifest["host"]["osBuild"]
assert manifest["host"]["architecture"] in {"arm64", "x86_64"}
assert manifest["developerDir"] == str(pathlib.Path(sys.argv[2]).resolve())
assert manifest["xcode"] == {"build": "99Z123", "version": "99.4"}
assert manifest["sdk"]["build"] == "99ZSDK1"
assert manifest["sdk"]["version"] == "99.4"
assert len(manifest["clang"]["identitySha256"]) == 64
assert manifest["wails"] == {"binary": None, "moduleVersion": None}
assert manifest["native"]["target"] is None
PY
grep -Eq '^cache_key=xcode-99\.4-99Z123-sdk-99\.4-99ZSDK1-clang-[0-9a-f]{64}$' \
  "$mock_github_output"

mock_full_manifest="$fixture_root/mock full build manifest.json"
DEVELOPER_DIR="$mock_developer_dir" \
KARTE_EXPECTED_XCODE_VERSION=99.4 \
KARTE_EXPECTED_XCODE_BUILD=99Z123 \
KARTE_EXPECTED_SDK_VERSION=99.4 \
KARTE_EXPECTED_SDK_BUILD=99ZSDK1 \
KARTE_TEST_DARWIN=1 \
XCODEBUILD_BIN="$mock_root/xcodebuild" \
XCRUN_BIN="$mock_root/xcrun" \
GO_BIN="$mock_root/go" \
KARTE_WAILS_BIN="$mock_root/wails fixture" \
KARTE_NATIVE_COLD=1 \
  "$script_dir/macos-wails.sh" \
    --native-root "$mock_native" \
    --target darwin-arm64 \
    --project-dir "$mock_project" \
    manifest "$mock_full_manifest" >/dev/null
python3 - "$mock_full_manifest" "$mock_wails_real" "$mock_native" <<'PY'
import json
import pathlib
import sys

manifest = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
assert manifest["wails"] == {
    "binary": sys.argv[2],
    "moduleVersion": "v2.13.0",
}
assert manifest["native"]["target"] == "darwin-arm64"
assert manifest["native"]["root"] == sys.argv[3]
assert manifest["native"]["coldBuild"] is True
assert len(manifest["native"]["stampSha256"]) == 64
PY

KARTE_TEST_EXPECTED_XCODE_VERSION=99.5
export KARTE_TEST_EXPECTED_XCODE_VERSION
expect_failure 'unexpected Xcode version' run_mock_wrapper >/dev/null
unset KARTE_TEST_EXPECTED_XCODE_VERSION

KARTE_TEST_EXPECTED_SDK_BUILD=99ZSDK2
export KARTE_TEST_EXPECTED_SDK_BUILD
expect_failure 'unexpected SDK build' run_mock_wrapper >/dev/null
unset KARTE_TEST_EXPECTED_SDK_BUILD

KARTE_TEST_WAILS_METADATA_VERSION=v2.12.0
export KARTE_TEST_WAILS_METADATA_VERSION
expect_failure 'Wails CLI module mismatch' run_mock_wrapper >/dev/null
unset KARTE_TEST_WAILS_METADATA_VERSION

cp "$mock_native/native-runtime.version" "$mock_native/native-runtime.version.valid"
printf '%s\n' 'tampered=true' >>"$mock_native/native-runtime.version"
expect_failure 'native runtime toolchain stamp mismatch' run_mock_wrapper >/dev/null
mv "$mock_native/native-runtime.version.valid" "$mock_native/native-runtime.version"

uti_source="$fixture_root/UTType link fixture.m"
cat >"$uti_source" <<'EOF'
#import <Foundation/Foundation.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>

int main(void) {
  @autoreleasepool {
    return [UTType class] == Nil;
  }
}
EOF

uti_negative_log="$fixture_root/uti-negative.log"
if DEVELOPER_DIR="$real_developer_dir" "$real_clang" \
  -fobjc-arc \
  -fno-autolink \
  -isysroot "$real_sdk_path" \
  -mmacosx-version-min=11.0 \
  "$uti_source" \
  -framework Foundation \
  -o "$fixture_root/uti-without-framework" \
  >"$uti_negative_log" 2>&1; then
  echo 'Expected UTType link failure without UniformTypeIdentifiers.framework' >&2
  exit 1
fi
grep -Fq '_OBJC_CLASS_$_UTType' "$uti_negative_log"

DEVELOPER_DIR="$real_developer_dir" "$real_clang" \
  -fobjc-arc \
  -fno-autolink \
  -isysroot "$real_sdk_path" \
  -mmacosx-version-min=11.0 \
  "$uti_source" \
  -framework Foundation \
  -framework UniformTypeIdentifiers \
  -o "$fixture_root/uti-with-framework"
"$fixture_root/uti-with-framework"

binding_project="$fixture_root/Wails bindings project with spaces"
binding_native="$binding_project/native runtime with spaces"
binding_runtime="$binding_native/sherpa/lib"
mkdir -p \
  "$binding_project/frontend/src" \
  "$binding_runtime" \
  "$binding_native/portaudio/lib/pkgconfig"

module_version=$(cd "$project_root" && "$real_go" list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)
wails_module_dir=$(cd "$project_root" && "$real_go" list -m -f '{{.Dir}}' github.com/wailsapp/wails/v2)
cat >"$binding_project/go.mod" <<EOF
module example.com/karte-wails-loader-fixture

go 1.25.0

require github.com/wailsapp/wails/v2 $module_version
EOF
cp "$wails_module_dir/go.sum" "$binding_project/go.sum"
chmod 0644 "$binding_project/go.sum"
grep -F "github.com/wailsapp/wails/v2 $module_version" "$project_root/go.sum" >>"$binding_project/go.sum"
cat >"$binding_project/wails.json" <<'EOF'
{
  "$schema": "https://wails.io/schemas/config.v2.json",
  "name": "karte-wails-loader-fixture",
  "outputfilename": "fixture",
  "wailsjsdir": "./frontend"
}
EOF
cat >"$binding_project/frontend/src/index.html" <<'EOF'
<!doctype html><title>fixture</title>
EOF
cat >"$binding_project/app.go" <<'EOF'
package main

type App struct{}

func (a *App) Ping() string { return "pong" }
EOF
cat >"$binding_project/main.go" <<'EOF'
package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/src
var assets embed.FS

func main() {
	_ = fixtureLoaderValue
	_ = wails.Run(&options.App{
		AssetServer: &assetserver.Options{Assets: assets},
		Bind:        []interface{}{&App{}},
	})
}
EOF
cat >"$binding_project/loader.go" <<'EOF'
package main

/*
#cgo darwin LDFLAGS: "-L${SRCDIR}/native runtime with spaces/sherpa/lib" -lkarte_binding_fixture
int karte_binding_fixture(void);
*/
import "C"

var fixtureLoaderValue = int(C.karte_binding_fixture())
EOF
cat >"$fixture_root/loader.c" <<'EOF'
int karte_binding_fixture(void) { return 86; }
EOF

DEVELOPER_DIR="$real_developer_dir" "$real_clang" \
  -dynamiclib \
  -isysroot "$real_sdk_path" \
  -mmacosx-version-min=11.0 \
  -Wl,-install_name,libkarte_binding_fixture.dylib \
  "$fixture_root/loader.c" \
  -o "$binding_runtime/libkarte_binding_fixture.dylib"
cp "$binding_runtime/libkarte_binding_fixture.dylib" "$binding_runtime/libonnxruntime.dylib"
cp "$binding_runtime/libkarte_binding_fixture.dylib" "$binding_runtime/libsherpa-onnx-c-api.dylib"
cp "$binding_runtime/libkarte_binding_fixture.dylib" "$binding_native/portaudio/lib/libportaudio.dylib"

real_wails="$fixture_root/verified tools/wails"
binding_go_cache="$fixture_root/bindings go cache"
mkdir -p "$(dirname "$real_wails")"
(
  cd "$wails_module_dir"
  GOCACHE="$fixture_root/go build cache" "$real_go" build -o "$real_wails" ./cmd/wails
)
real_wails_metadata_path=$(realpath "$real_wails")
metadata_go="$fixture_root/verified tools/go-with-version-metadata"
cat >"$metadata_go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${1:-} == version && ${2:-} == -m && ${3:-} == "$KARTE_TEST_REAL_WAILS" ]]; then
  "$KARTE_TEST_REAL_GO" version -m "$3" |
    awk -v version="$KARTE_TEST_WAILS_VERSION" '
      $1 == "mod" && $2 == "github.com/wailsapp/wails/v2" { $3 = version }
      { print }
    '
  exit 0
fi
exec "$KARTE_TEST_REAL_GO" "$@"
EOF
chmod +x "$metadata_go"
(
  cd "$binding_project"
  GOCACHE="$binding_go_cache" "$real_go" mod tidy
)

actual_xcode_output=$(DEVELOPER_DIR="$real_developer_dir" xcodebuild -version)
actual_xcode_version=$(printf '%s\n' "$actual_xcode_output" | awk 'NR == 1 { print $2 }')
actual_xcode_build=$(printf '%s\n' "$actual_xcode_output" | awk 'NR == 2 { print $3 }')
actual_sdk_version=$(DEVELOPER_DIR="$real_developer_dir" xcrun --sdk macosx --show-sdk-version)
actual_sdk_build=$(DEVELOPER_DIR="$real_developer_dir" xcrun --sdk macosx --show-sdk-build-version)
case $(uname -m) in
  arm64) binding_target=darwin-arm64; binding_arch=arm64 ;;
  x86_64) binding_target=darwin-amd64; binding_arch=x86_64 ;;
  *)
    echo "Unsupported macOS fixture architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

(
  export DEVELOPER_DIR=$real_developer_dir
  export KARTE_EXPECTED_XCODE_VERSION=$actual_xcode_version
  export KARTE_EXPECTED_XCODE_BUILD=$actual_xcode_build
  export KARTE_EXPECTED_SDK_VERSION=$actual_sdk_version
  export KARTE_EXPECTED_SDK_BUILD=$actual_sdk_build
  # shellcheck source=scripts/lib/macos-toolchain.sh
  source "$script_dir/lib/macos-toolchain.sh"
  karte_load_macos_toolchain
  karte_expected_native_stamp "$binding_arch" 11.0 >"$binding_native/native-runtime.version"
)

binding_negative_log="$fixture_root/bindings-negative.log"
if (
  cd "$binding_project"
  env -u DYLD_LIBRARY_PATH \
    DEVELOPER_DIR="$real_developer_dir" \
    GOCACHE="$binding_go_cache" \
    MACOSX_DEPLOYMENT_TARGET=11.0 \
    CGO_CFLAGS=-mmacosx-version-min=11.0 \
    CGO_CXXFLAGS=-mmacosx-version-min=11.0 \
    CGO_LDFLAGS='-framework UniformTypeIdentifiers -mmacosx-version-min=11.0' \
    "$real_wails" generate module
) >"$binding_negative_log" 2>&1; then
  echo 'Expected Wails bindings child to fail without its loader path' >&2
  exit 1
fi
if ! grep -Fq 'libkarte_binding_fixture.dylib' "$binding_negative_log"; then
  echo 'Wails bindings negative case did not fail at the fixture loader' >&2
  sed -n '1,160p' "$binding_negative_log" >&2
  exit 1
fi

DEVELOPER_DIR="$real_developer_dir" \
KARTE_EXPECTED_XCODE_VERSION="$actual_xcode_version" \
KARTE_EXPECTED_XCODE_BUILD="$actual_xcode_build" \
KARTE_EXPECTED_SDK_VERSION="$actual_sdk_version" \
KARTE_EXPECTED_SDK_BUILD="$actual_sdk_build" \
KARTE_WAILS_BIN="$real_wails" \
GO_BIN="$metadata_go" \
KARTE_TEST_REAL_GO="$real_go" \
KARTE_TEST_REAL_WAILS="$real_wails_metadata_path" \
KARTE_TEST_WAILS_VERSION="$module_version" \
GOCACHE="$binding_go_cache" \
  "$script_dir/macos-wails.sh" \
    --native-root "$binding_native" \
    --target "$binding_target" \
    --project-dir "$binding_project" \
    generate

[[ -f "$binding_project/frontend/wailsjs/go/main/App.js" ]] || {
  echo 'Wails bindings child did not generate the expected App binding' >&2
  exit 1
}

ruby -ryaml - "$project_root/.github/workflows/ci.yml" <<'RUBY'
workflow_path = ARGV.fetch(0)
walk = nil
walk = lambda do |node|
  if node.is_a?(Psych::Nodes::Mapping)
    seen = {}
    node.children.each_slice(2) do |key, value|
      if key.is_a?(Psych::Nodes::Scalar)
        raise "duplicate YAML key #{key.value.inspect} at line #{key.start_line + 1}" if seen.key?(key.value)
        seen[key.value] = true
      end
      walk.call(key)
      walk.call(value)
    end
  else
    Array(node.children).each { |child| walk.call(child) }
  end
end
walk.call(Psych.parse_file(workflow_path))
workflow = YAML.load_file(workflow_path)
build = workflow.fetch("jobs").fetch("build")
rows = build.fetch("strategy").fetch("matrix").fetch("include")
mac_rows = rows.select { |row| row.fetch("os").start_with?("macos-") }
raise "expected three macOS matrix rows" unless mac_rows.length == 3

expected = {
  "macos-15" => ["darwin-arm64", "/Applications/Xcode_16.4.app/Contents/Developer", "16.4", "16F6", "15.5", "24F74", "0", true],
  "macos-15-intel" => ["darwin-amd64", "/Applications/Xcode_16.4.app/Contents/Developer", "16.4", "16F6", "15.5", "24F74", "0", true],
  "macos-26" => ["darwin-arm64", "/Applications/Xcode_26.0.1.app/Contents/Developer", "26.0.1", "17A400", "26.0", "25A352", "1", false],
}
mac_rows.each do |row|
  actual = %w[target developer_dir xcode_version xcode_build sdk_version sdk_build native_cold].map { |key| row.fetch(key).to_s }
  actual << row.fetch("publish_artifact")
  raise "unexpected macOS matrix contract for #{row.fetch('os')}: #{actual.inspect}" unless actual == expected.fetch(row.fetch("os"))
end

steps = build.fetch("steps").to_h { |step| [step.fetch("name"), step] }
raise "build artifact publication is not gated" unless steps.fetch("Upload build artifact").fetch("if") == "matrix.publish_artifact"
raise "startup diagnostics are not retained on failure" unless steps.fetch("Upload startup smoke diagnostics").fetch("if") == "always()"
raise "macOS wrapper fixture is not in CI" unless steps.key?("Test macOS Wails toolchain，UTType，and loader contracts")
raise "toolchain manifest evidence is not retained" unless steps.fetch("Upload macOS toolchain manifest").fetch("if").include?("always()")
RUBY

echo 'macOS Wails toolchain，UTType link，and bindings loader tests passed'
