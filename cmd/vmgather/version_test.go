package main

import "testing"

func TestVersionIsVariable(t *testing.T) {
	// This test is intentionally simple: it guarantees that `version` is a `var`,
	// so it can be overridden via `-ldflags "-X main.version=..."` in release builds.
	old := version
	version = "test"
	version = old
}

