#!/usr/bin/env bash

# Source-integrity helpers for the pinned macOS native build．Callers must
# enable their own shell options before sourcing this file．

karte_assert_clean_git_checkout() {
  local destination=$1
  local expected_commit=$2
  local git_bin=${GIT_BIN:-git}
  local actual_commit status

  [[ -d "$destination/.git" && ! -L "$destination/.git" ]] || {
    echo "Pinned Git source is not a regular checkout: $destination" >&2
    return 1
  }
  actual_commit=$("$git_bin" -C "$destination" rev-parse --verify HEAD) || {
    echo "Cannot resolve pinned Git source HEAD: $destination" >&2
    return 1
  }
  [[ "$actual_commit" == "$expected_commit" ]] || {
    echo "Unexpected source commit in $destination: $actual_commit，expected $expected_commit" >&2
    return 1
  }
  status=$("$git_bin" -C "$destination" status \
    --porcelain=v1 \
    --untracked-files=all \
    --ignore-submodules=none) || {
    echo "Cannot inspect pinned Git source status: $destination" >&2
    return 1
  }
  [[ -z "$status" ]] || {
    echo "Pinned Git source contains tracked，staged，submodule，or untracked changes: $destination" >&2
    printf '%s\n' "$status" >&2
    return 1
  }
}

karte_extract_archive_fresh() {
  local archive=$1
  local extraction_root=$2
  local expected_directory=$3
  local tar_bin=${TAR_BIN:-tar}

  [[ -f "$archive" && ! -L "$archive" ]] || {
    echo "Verified source archive is missing or not regular: $archive" >&2
    return 1
  }
  [[ -d "$extraction_root" && ! -L "$extraction_root" ]] || {
    echo "Fresh source extraction root is missing，not a directory，or a symlink: $extraction_root" >&2
    return 1
  }
  case "$expected_directory" in
    ""|.|..|*/*)
      echo "Expected archive directory must be one safe path component: $expected_directory" >&2
      return 1
      ;;
  esac
  [[ ! -e "$extraction_root/$expected_directory" && ! -L "$extraction_root/$expected_directory" ]] || {
    echo "Refusing to reuse a stale extracted source tree: $extraction_root/$expected_directory" >&2
    return 1
  }

  "$tar_bin" -xzf "$archive" -C "$extraction_root" || {
    echo "Cannot extract verified source archive: $archive" >&2
    return 1
  }
  [[ -d "$extraction_root/$expected_directory" && ! -L "$extraction_root/$expected_directory" ]] || {
    echo "Verified archive did not produce the expected source directory: $expected_directory" >&2
    return 1
  }
}
