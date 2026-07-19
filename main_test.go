package main

import (
	"os"
	"strings"
	"testing"
)

func TestAPILogBodiesRequiresAPILogPath(t *testing.T) {
	err := run([]string{"--api-log-bodies"})
	if err == nil || !strings.Contains(err.Error(), "--api-log-bodies requires --api-log PATH") {
		t.Fatalf("run error = %v", err)
	}
}

func TestAPILogFlagIsParsedBeforeVersionExit(t *testing.T) {
	path := t.TempDir() + "/api.jsonl"
	if err := run([]string{"--api-log", path, "--version"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("version-only invocation unexpectedly created API log: %v", err)
	}
}
