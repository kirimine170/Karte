#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
app_bundle=${1:?usage: package-macos-runtime.sh APP_BUNDLE TARGET}
target=${2:?usage: package-macos-runtime.sh APP_BUNDLE TARGET}
supported_macos_version=${MACOSX_DEPLOYMENT_TARGET:-11.0}

case "$target" in
  darwin-arm64)
    expected_arch=arm64
    ;;
  darwin-amd64)
    expected_arch=x86_64
    ;;
  *)
    echo "Unsupported macOS target: $target" >&2
    exit 1
    ;;
esac

if [[ ! -d "$app_bundle/Contents/MacOS" ]]; then
  echo "macOS app bundle not found: $app_bundle" >&2
  exit 1
fi

frameworks_dir="$app_bundle/Contents/Frameworks"
mkdir -p "$frameworks_dir"

if [[ -z ${KARTE_SHERPA_LIB_DIR:-} ]]; then
  echo "KARTE_SHERPA_LIB_DIR is required; build pinned macOS native libraries with scripts/build-macos-native-runtime.sh" >&2
  exit 1
fi
if [[ -z ${KARTE_PORTAUDIO_LIB_DIR:-} ]]; then
  echo "KARTE_PORTAUDIO_LIB_DIR is required; Homebrew bottles are not valid release inputs" >&2
  exit 1
fi
sherpa_lib_dir=$KARTE_SHERPA_LIB_DIR
portaudio_lib_dir=$KARTE_PORTAUDIO_LIB_DIR
native_root=$(cd "$sherpa_lib_dir/../.." && pwd -P)
native_license_dir="$native_root/licenses"

for native_license in onnxruntime-LICENSE sherpa-onnx-LICENSE portaudio-LICENSE.txt; do
  if [[ ! -f "$native_license_dir/$native_license" || -L "$native_license_dir/$native_license" ]]; then
    echo "Pinned native license is missing or not regular: $native_license_dir/$native_license" >&2
    exit 1
  fi
done

bundled_license_dir="$app_bundle/Contents/Resources/THIRD_PARTY_LICENSES/native"
mkdir -p "$bundled_license_dir"
cp -p "$native_license_dir/onnxruntime-LICENSE" "$bundled_license_dir/onnxruntime-LICENSE"
cp -p "$native_license_dir/sherpa-onnx-LICENSE" "$bundled_license_dir/sherpa-onnx-LICENSE"
cp -p "$native_license_dir/portaudio-LICENSE.txt" "$bundled_license_dir/portaudio-LICENSE.txt"
chmod 0644 "$bundled_license_dir"/*

copy_dylibs() {
  local source_dir=$1
  local label=$2
  local copied=0
  local source
  local destination
  local link_target

  if [[ ! -d "$source_dir" ]]; then
    echo "$label library directory not found: $source_dir" >&2
    exit 1
  fi

  while IFS= read -r -d '' source; do
    destination="$frameworks_dir/$(basename "$source")"
    if [[ -L "$source" ]]; then
      link_target=$(readlink "$source")
      ln -sfn "$(basename "$link_target")" "$destination"
    else
      cp -p "$source" "$destination"
      chmod u+w "$destination"
    fi
    copied=$((copied + 1))
  done < <(find "$source_dir" -maxdepth 1 -name '*.dylib' -print0)

  if [[ $copied -eq 0 ]]; then
    echo "No $label dylibs found in $source_dir" >&2
    exit 1
  fi
}

copy_dylibs "$sherpa_lib_dir" "sherpa-onnx"
copy_dylibs "$portaudio_lib_dir" "PortAudio"

info_plist="$app_bundle/Contents/Info.plist"
executable_name=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$info_plist")
app_executable="$app_bundle/Contents/MacOS/$executable_name"
if [[ ! -f "$app_executable" ]]; then
  echo "App executable not found: $app_executable" >&2
  exit 1
fi

is_macho() {
  file "$1" | grep -q 'Mach-O'
}

list_rpaths() {
  otool -l "$1" | awk '
    $1 == "cmd" && $2 == "LC_RPATH" {
      getline
      getline
      print $2
    }
  '
}

remove_build_host_rpaths() {
  local binary=$1
  local rpath

  while IFS= read -r rpath; do
    case "$rpath" in
      /Users/*|/opt/homebrew/*|/usr/local/*)
        install_name_tool -delete_rpath "$rpath" "$binary"
        ;;
    esac
  done < <(list_rpaths "$binary")
}

rewrite_bundled_dependencies() {
  local binary=$1
  local dependency
  local dependency_name

  while IFS= read -r dependency; do
    dependency=${dependency#${dependency%%[![:space:]]*}}
    dependency=${dependency%% *}
    [[ -n "$dependency" ]] || continue

    dependency_name=$(basename "$dependency")
    if [[ -e "$frameworks_dir/$dependency_name" ]]; then
      install_name_tool -change "$dependency" "@rpath/$dependency_name" "$binary"
    fi
  done < <(otool -L "$binary" | tail -n +2)
}

while IFS= read -r -d '' binary; do
  is_macho "$binary" || continue
  if [[ "$binary" == *.dylib ]]; then
    install_name_tool -id "@rpath/$(basename "$binary")" "$binary"
  fi
  remove_build_host_rpaths "$binary"
  rewrite_bundled_dependencies "$binary"
done < <(find "$app_bundle/Contents" -type f -print0)

if ! list_rpaths "$app_executable" | grep -Fxq '@executable_path/../Frameworks'; then
  install_name_tool -add_rpath '@executable_path/../Frameworks' "$app_executable"
fi

verify_macho() {
  local binary=$1
  local dependency
  local dependency_name
  local rpath
  local arches

  arches=$(lipo -archs "$binary")
  case " $arches " in
    *" $expected_arch "*) ;;
    *)
      echo "Wrong architecture for $binary: expected $expected_arch, found $arches" >&2
      return 1
      ;;
  esac

  while IFS= read -r rpath; do
    case "$rpath" in
      /Users/*|/opt/homebrew/*|/usr/local/*)
        echo "Build-host RPATH remains in $binary: $rpath" >&2
        return 1
        ;;
    esac
  done < <(list_rpaths "$binary")

  while IFS= read -r dependency; do
    dependency=${dependency#${dependency%%[![:space:]]*}}
    dependency=${dependency%% *}
    [[ -n "$dependency" ]] || continue

    case "$dependency" in
      /System/Library/*|/usr/lib/*)
        ;;
      @rpath/*)
        dependency_name=$(basename "$dependency")
        if [[ ! -e "$frameworks_dir/$dependency_name" ]]; then
          echo "Bundled dependency is missing for $binary: $dependency" >&2
          return 1
        fi
        ;;
      *)
        echo "Unportable dependency remains in $binary: $dependency" >&2
        return 1
        ;;
    esac
  done < <(otool -L "$binary" | tail -n +2)
}

while IFS= read -r -d '' binary; do
  is_macho "$binary" || continue
  verify_macho "$binary"
done < <(find "$app_bundle/Contents" -type f -print0)

model_count=0
while IFS= read -r -d '' model; do
  model_count=$((model_count + 1))
  if head -c 200 "$model" | grep -aq 'version https://git-lfs.github.com/spec/v1'; then
    echo "Git LFS pointer was packaged instead of an ONNX model: $model" >&2
    exit 1
  fi
done < <(find "$app_bundle/Contents/Resources" -type f -name '*.onnx' -print0)

if [[ $model_count -eq 0 ]]; then
  echo "No ONNX models were packaged in $app_bundle" >&2
  exit 1
fi

"$script_dir/capture-macos-native-compliance.sh" "$app_bundle" "$target"

"$script_dir/verify-macos-deployment-target.sh" "$app_bundle" "$supported_macos_version"

codesign_identity=${MACOS_CODESIGN_IDENTITY:--}
while IFS= read -r -d '' dylib; do
  codesign --force --sign "$codesign_identity" --timestamp=none "$dylib"
done < <(find "$app_bundle/Contents" -type f -name '*.dylib' -print0)

codesign --force --deep --sign "$codesign_identity" --timestamp=none "$app_bundle"
codesign --verify --deep --strict --verbose=2 "$app_bundle"

echo "Bundled and verified macOS runtime dependencies in $app_bundle"
