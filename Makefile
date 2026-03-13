VERSION := 0.1.0
BINARY  := distrorun
GOFLAGS := -ldflags="-s -w"

.PHONY: build clean rpm deb all

## build: Compile the Go binary
build:
	go build $(GOFLAGS) -o $(BINARY) .

## rpm: Build an RPM package
rpm: build
	nfpm pkg --packager rpm --target $(BINARY)-$(VERSION).x86_64.rpm

## deb: Build a DEB package
deb: build
	nfpm pkg --packager deb --target $(BINARY)_$(VERSION)_amd64.deb

## all: Build binary + RPM + DEB
all: build rpm deb

## clean: Remove build artifacts
clean:
	rm -f $(BINARY) *.rpm *.deb *.iso *.qcow2 *-sbom.spdx.json

## help: Show available targets
help:
	@grep -E '^##' Makefile | sed 's/## //'
