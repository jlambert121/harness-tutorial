#!/bin/sh
set -e
test -z "$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
go vet ./...
go test -race -count=2 ./... 2>&1
