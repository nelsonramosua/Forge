#!/usr/bin/env bash
set -euo pipefail

make clean
make build
make test
go test -race ./...
go vet ./...
./bin/forge-build-runner --workdir /tmp --cgroup smoke -- /bin/sh -lc /bin/true
