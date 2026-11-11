#!/usr/bin/env bash
# Build the Lambda zip: build/function.zip containing one file, "bootstrap".
# Lambda's "provided.al2023" runtime runs the executable named bootstrap.
# Run from the repo root:  bash scripts/build.sh
set -euo pipefail
cd "$(dirname "$0")/.."

mkdir -p build
rm -f build/bootstrap build/function.zip
# Cross-compile for Lambda (Linux, arm64) from any machine. CGO off = one
# static binary with no C libraries to ship.
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o build/bootstrap ./cmd/lambda
(cd build && zip -q function.zip bootstrap)
echo "built build/function.zip ($(wc -c < build/function.zip | tr -d ' ') bytes)"
