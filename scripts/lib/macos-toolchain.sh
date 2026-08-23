#!/usr/bin/env bash

# Shared，source-only helpers for the reproducible macOS build entrypoints．
# Callers must enable their own shell options before sourcing this file．

karte_toolchain_fail() {
  echo "macOS toolchain verification failed: $*" >&2
  return 1
}

karte_require_identifier() {
  local name=$1
  local value=$2
  if [[ -z "$value" || ! "$value" =~ ^[A-Za-z0-9._+-]+$ ]]; then
    karte_toolchain_fail "$name is missing or invalid: $value"
    return 1
  fi
}

karte_load_macos_toolchain() {
  local xcodebuild_bin=${XCODEBUILD_BIN:-xcodebuild}
  local xcrun_bin=${XCRUN_BIN:-xcrun}
  local realpath_bin=${REALPATH_BIN:-realpath}
  local shasum_bin=${SHASUM_BIN:-shasum}
  local xcode_output xcode_line build_line sdk_path clang_output toolchain_payload

  [[ $(uname -s) == Darwin || ${KARTE_TEST_DARWIN:-0} == 1 ]] || {
    karte_toolchain_fail "Darwin host is required"
    return 1
  }
  [[ -n ${DEVELOPER_DIR:-} && "$DEVELOPER_DIR" == /* ]] || {
    karte_toolchain_fail "DEVELOPER_DIR must be an explicit absolute path"
    return 1
  }
  [[ -d "$DEVELOPER_DIR" && ! -L "$DEVELOPER_DIR" ]] || {
    karte_toolchain_fail "DEVELOPER_DIR is missing，not a directory，or a symlink: $DEVELOPER_DIR"
    return 1
  }
  for expected_name in \
    KARTE_EXPECTED_XCODE_VERSION \
    KARTE_EXPECTED_XCODE_BUILD \
    KARTE_EXPECTED_SDK_VERSION; do
    [[ -n ${!expected_name:-} ]] || {
      karte_toolchain_fail "$expected_name is required"
      return 1
    }
  done

  command -v "$xcodebuild_bin" >/dev/null 2>&1 || {
    karte_toolchain_fail "xcodebuild was not found: $xcodebuild_bin"
    return 1
  }
  command -v "$xcrun_bin" >/dev/null 2>&1 || {
    karte_toolchain_fail "xcrun was not found: $xcrun_bin"
    return 1
  }
  command -v "$realpath_bin" >/dev/null 2>&1 || {
    karte_toolchain_fail "realpath was not found: $realpath_bin"
    return 1
  }
  command -v "$shasum_bin" >/dev/null 2>&1 || {
    karte_toolchain_fail "shasum was not found: $shasum_bin"
    return 1
  }

  xcode_output=$(DEVELOPER_DIR="$DEVELOPER_DIR" "$xcodebuild_bin" -version) || {
    karte_toolchain_fail "xcodebuild -version failed"
    return 1
  }
  [[ $(printf '%s\n' "$xcode_output" | awk 'NF { count++ } END { print count + 0 }') == 2 ]] || {
    karte_toolchain_fail "xcodebuild -version returned an unexpected shape"
    return 1
  }
  xcode_line=$(printf '%s\n' "$xcode_output" | awk 'NF { print; exit }')
  build_line=$(printf '%s\n' "$xcode_output" | awk 'NF { count++; if (count == 2) print }')
  [[ "$xcode_line" == Xcode\ * && "$build_line" == Build\ version\ * ]] || {
    karte_toolchain_fail "xcodebuild -version returned unexpected labels"
    return 1
  }
  KARTE_XCODE_VERSION=${xcode_line#Xcode }
  KARTE_XCODE_BUILD=${build_line#Build version }

  KARTE_SDK_VERSION=$(DEVELOPER_DIR="$DEVELOPER_DIR" "$xcrun_bin" --sdk macosx --show-sdk-version) || {
    karte_toolchain_fail "cannot read macOS SDK version"
    return 1
  }
  KARTE_SDK_BUILD=$(DEVELOPER_DIR="$DEVELOPER_DIR" "$xcrun_bin" --sdk macosx --show-sdk-build-version) || {
    karte_toolchain_fail "cannot read macOS SDK build"
    return 1
  }
  sdk_path=$(DEVELOPER_DIR="$DEVELOPER_DIR" "$xcrun_bin" --sdk macosx --show-sdk-path) || {
    karte_toolchain_fail "cannot locate macOS SDK"
    return 1
  }
  KARTE_CLANG_PATH=$(DEVELOPER_DIR="$DEVELOPER_DIR" "$xcrun_bin" --sdk macosx --find clang) || {
    karte_toolchain_fail "cannot locate Xcode clang"
    return 1
  }

  KARTE_DEVELOPER_DIR_REAL=$($realpath_bin "$DEVELOPER_DIR") || {
    karte_toolchain_fail "cannot resolve DEVELOPER_DIR: $DEVELOPER_DIR"
    return 1
  }
  KARTE_SDK_PATH=$($realpath_bin "$sdk_path") || {
    karte_toolchain_fail "cannot resolve macOS SDK path: $sdk_path"
    return 1
  }
  KARTE_CLANG_PATH=$($realpath_bin "$KARTE_CLANG_PATH") || {
    karte_toolchain_fail "cannot resolve Xcode clang path"
    return 1
  }
  case "$KARTE_SDK_PATH" in
    "$KARTE_DEVELOPER_DIR_REAL"/*) ;;
    *)
      karte_toolchain_fail "macOS SDK is outside DEVELOPER_DIR: $KARTE_SDK_PATH"
      return 1
      ;;
  esac
  case "$KARTE_CLANG_PATH" in
    "$KARTE_DEVELOPER_DIR_REAL"/*) ;;
    *)
      karte_toolchain_fail "clang is outside DEVELOPER_DIR: $KARTE_CLANG_PATH"
      return 1
      ;;
  esac

  clang_output=$(DEVELOPER_DIR="$DEVELOPER_DIR" "$KARTE_CLANG_PATH" --version) || {
    karte_toolchain_fail "Xcode clang --version failed"
    return 1
  }
  [[ -n "$clang_output" ]] || {
    karte_toolchain_fail "Xcode clang identity is empty"
    return 1
  }
  KARTE_CLANG_IDENTITY=$clang_output
  KARTE_CLANG_IDENTITY_SHA256=$(printf '%s' "$clang_output" | "$shasum_bin" -a 256 | awk '{print $1}') || {
    karte_toolchain_fail "cannot hash Xcode clang identity"
    return 1
  }

  karte_require_identifier xcode-version "$KARTE_XCODE_VERSION" || return 1
  karte_require_identifier xcode-build "$KARTE_XCODE_BUILD" || return 1
  karte_require_identifier sdk-version "$KARTE_SDK_VERSION" || return 1
  karte_require_identifier sdk-build "$KARTE_SDK_BUILD" || return 1
  karte_require_identifier clang-sha256 "$KARTE_CLANG_IDENTITY_SHA256" || return 1

  [[ "$KARTE_XCODE_VERSION" == "$KARTE_EXPECTED_XCODE_VERSION" ]] || {
    karte_toolchain_fail "Xcode $KARTE_XCODE_VERSION does not match expected $KARTE_EXPECTED_XCODE_VERSION"
    return 1
  }
  [[ "$KARTE_XCODE_BUILD" == "$KARTE_EXPECTED_XCODE_BUILD" ]] || {
    karte_toolchain_fail "Xcode build $KARTE_XCODE_BUILD does not match expected $KARTE_EXPECTED_XCODE_BUILD"
    return 1
  }
  [[ "$KARTE_SDK_VERSION" == "$KARTE_EXPECTED_SDK_VERSION" ]] || {
    karte_toolchain_fail "macOS SDK $KARTE_SDK_VERSION does not match expected $KARTE_EXPECTED_SDK_VERSION"
    return 1
  }
  if [[ -n ${KARTE_EXPECTED_SDK_BUILD:-} && "$KARTE_SDK_BUILD" != "$KARTE_EXPECTED_SDK_BUILD" ]]; then
    karte_toolchain_fail "macOS SDK build $KARTE_SDK_BUILD does not match expected $KARTE_EXPECTED_SDK_BUILD"
    return 1
  fi

  toolchain_payload=$(printf '%s\n' \
    "xcode_version=$KARTE_XCODE_VERSION" \
    "xcode_build=$KARTE_XCODE_BUILD" \
    "sdk_version=$KARTE_SDK_VERSION" \
    "sdk_build=$KARTE_SDK_BUILD" \
    "clang_identity_sha256=$KARTE_CLANG_IDENTITY_SHA256")
  KARTE_TOOLCHAIN_SHA256=$(printf '%s' "$toolchain_payload" | "$shasum_bin" -a 256 | awk '{print $1}') || {
    karte_toolchain_fail "cannot hash macOS toolchain identity"
    return 1
  }
  KARTE_TOOLCHAIN_CACHE_KEY="xcode-${KARTE_XCODE_VERSION}-${KARTE_XCODE_BUILD}-sdk-${KARTE_SDK_VERSION}-${KARTE_SDK_BUILD}-clang-${KARTE_CLANG_IDENTITY_SHA256}"

  export KARTE_XCODE_VERSION KARTE_XCODE_BUILD KARTE_SDK_VERSION KARTE_SDK_BUILD
  export KARTE_DEVELOPER_DIR_REAL KARTE_SDK_PATH KARTE_CLANG_PATH
  export KARTE_CLANG_IDENTITY KARTE_CLANG_IDENTITY_SHA256
  export KARTE_TOOLCHAIN_SHA256 KARTE_TOOLCHAIN_CACHE_KEY
}

karte_expected_native_stamp() {
  local architecture=$1
  local deployment_target=$2
  cat <<EOF
format=2
sherpa=1.13.4@142807252687d81b40d6315f23470a1512a00de3
onnxruntime=1.27.0@8f0278c77bf44b0cc83c098c6c722b92a36ac4b5
portaudio=19.7.0@47efbf42c77c19a05d22e627d42873e991ec0c1357219c0d74ce6a2948cb2def
provider=cpu
arch=$architecture
minos=$deployment_target
xcode_version=$KARTE_XCODE_VERSION
xcode_build=$KARTE_XCODE_BUILD
sdk_version=$KARTE_SDK_VERSION
sdk_build=$KARTE_SDK_BUILD
clang_identity_sha256=$KARTE_CLANG_IDENTITY_SHA256
EOF
}
