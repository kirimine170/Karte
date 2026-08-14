#!/usr/bin/env bash
set -euo pipefail

model_name="sherpa-onnx-streaming-zipformer-ar_en_id_ja_ru_th_vi_zh-2025-02-10"
archive_name="${model_name}.tar.bz2"
archive_url="https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/${archive_name}"
archive_sha256="28044b67324f7f831689f0a3761473dd2ade380e93aa53f1dbcd479ef71c40d4"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
project_root="$(cd "${script_dir}/.." && pwd)"
target_dir="${KARTE_ASR_MODEL_TARGET_DIR:-${project_root}/templates/karte_data_template/data/asr/${model_name}}"
cache_root="${KARTE_ASR_CACHE_DIR:-${TMPDIR:-/tmp}/karte-asr-models}"
archive_path="${KARTE_ASR_MODEL_ARCHIVE:-${cache_root}/${archive_name}}"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
    return
  fi
  echo "No SHA-256 command is available" >&2
  return 1
}

verify_sha256() {
  local path="$1"
  local expected="$2"
  local actual
  actual="$(sha256_file "$path")"
  if [[ "$actual" != "$expected" ]]; then
    echo "SHA-256 mismatch for ${path}: got ${actual}, want ${expected}" >&2
    return 1
  fi
}

download_archive() {
  mkdir -p "$(dirname "$archive_path")"
  local partial
  local -a curl_args=(--fail --location --retry 3 --silent --show-error)
  if curl --help all 2>/dev/null | grep -q -- '--retry-all-errors'; then
    curl_args+=(--retry-all-errors)
  fi
  partial="$(mktemp "${archive_path}.part.XXXXXX")"
  if ! curl "${curl_args[@]}" --output "$partial" "$archive_url"; then
    rm -f -- "$partial"
    return 1
  fi
  if ! verify_sha256 "$partial" "$archive_sha256"; then
    rm -f -- "$partial"
    return 1
  fi
  mv "$partial" "$archive_path"
}

if [[ ! -f "$archive_path" ]]; then
  download_archive
elif ! verify_sha256 "$archive_path" "$archive_sha256"; then
  echo "Replacing invalid cached ASR archive: ${archive_path}" >&2
  rm -f -- "$archive_path"
  download_archive
fi

extract_root="$(mktemp -d "${TMPDIR:-/tmp}/karte-asr-models.XXXXXX")"
trap 'rm -rf -- "$extract_root"' EXIT
tar -xjf "$archive_path" -C "$extract_root"
source_dir="${extract_root}/${model_name}"

declare -a model_files=(
  "decoder-epoch-75-avg-11-chunk-16-left-128.onnx:7ebc63f34b21c8efb4a41a5a2eee7fe1448829ce0230ecc5369e67fc14d90d48"
  "encoder-epoch-75-avg-11-chunk-16-left-128.int8.onnx:f9001ed7a9e46d0294438c1a30cd7c72d1cc4bdd4e7880edbcda36f67081e32e"
  "joiner-epoch-75-avg-11-chunk-16-left-128.int8.onnx:db88e3172323551abaa99b91b18fb422a27ea4a834fd0db10389f9478816f917"
)

mkdir -p "$target_dir"
for entry in "${model_files[@]}"; do
  filename="${entry%%:*}"
  expected="${entry#*:}"
  source_path="${source_dir}/${filename}"
  if [[ ! -f "$source_path" ]]; then
    echo "Pinned ASR archive is missing ${filename}" >&2
    exit 1
  fi
  verify_sha256 "$source_path" "$expected"
  cp "$source_path" "${target_dir}/${filename}"
  chmod 0644 "${target_dir}/${filename}"
done

echo "Installed verified ASR models in ${target_dir}"
