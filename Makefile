GO ?= go
CC ?= gcc
CFLAGS ?= -std=c11 -Wall -Wextra -Werror -O2
PREFIX ?= /usr/local

.PHONY: all build test clean run-control-plane run-agent install

all: build

build: bin/forge-control-plane bin/forge-exporter bin/forge-agent bin/forge-build-runner

bin:
	mkdir -p bin

bin/forge-control-plane: go.mod control-plane/cmd/forge-control-plane/main.go $(shell find control-plane -name '*.go')
	mkdir -p bin
	$(GO) build -o $@ ./control-plane/cmd/forge-control-plane

bin/forge-exporter: go.mod cmd/forge-exporter/main.go
	mkdir -p bin
	$(GO) build -o $@ ./cmd/forge-exporter

bin/forge-agent: agent/src/forge_agent.c
	mkdir -p bin
	$(CC) $(CFLAGS) -pthread -o $@ $< agent/src/json_parser.c

bin/forge-build-runner: build-runner/src/forge_build_runner.c
	mkdir -p bin
	$(CC) $(CFLAGS) -o $@ $<

test: build
	$(GO) test ./...

run-control-plane: bin/forge-control-plane
	./bin/forge-control-plane

run-agent: bin/forge-agent bin/forge-build-runner
	FORGE_RUNNER_PATH=./bin/forge-build-runner ./bin/forge-agent

install: build
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 0755 bin/forge-control-plane $(DESTDIR)$(PREFIX)/bin/forge-control-plane
	install -m 0755 bin/forge-exporter $(DESTDIR)$(PREFIX)/bin/forge-exporter
	install -m 0755 bin/forge-agent $(DESTDIR)$(PREFIX)/bin/forge-agent
	install -m 0755 bin/forge-build-runner $(DESTDIR)$(PREFIX)/bin/forge-build-runner

clean:
	rm -rf bin data
