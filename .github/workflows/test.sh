#!/usr/bin/env bash

set -o errexit -o nounset -o pipefail

# Confirm that go.mod and go.sum are tidy.
cp go.mod go.mod.orig
cp go.sum go.sum.orig
go mod tidy
# Use -w to ignore differences in OS newlines.
diff -w go.mod.orig go.mod || { echo "go.mod is not tidy"; exit 1; }
diff -w go.sum.orig go.sum || { echo "go.sum is not tidy"; exit 1; }
rm go.mod.orig go.sum.orig

# Run tests
go test ./...

# Static analysis
go vet ./...
