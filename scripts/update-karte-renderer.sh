#!/usr/bin/env bash
set -euo pipefail

module_path="github.com/kirimine170/KarteRenderer"
repository_path="github.com/kirimine170/Karte_renderer"

latest_version="$(GOFLAGS=-mod=mod go list -m -f '{{.Version}}' "${repository_path}@main")"
if [[ -z "${latest_version}" ]]; then
  echo "failed to resolve the latest Karte Renderer version" >&2
  exit 1
fi

echo "Updating Karte Renderer to ${latest_version}"
GOFLAGS=-mod=mod go mod edit -replace="${module_path}=${repository_path}@${latest_version}"
GOFLAGS=-mod=mod go mod tidy

echo "Testing Karte Renderer dependency"
GOFLAGS=-mod=readonly go test "${module_path}/..."

echo "Testing Karte"
mkdir -p frontend/dist
touch frontend/dist/.placeholder
GOFLAGS=-mod=readonly go test ./...
