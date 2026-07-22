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

func TestAllProjectsModeDerivation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"global admin, no project → all projects", []string{"--global-admin"}, true},
		{"global admin with project → scoped", []string{"--global-admin", "--project", "demo"}, false},
		{"project only → scoped", []string{"--project", "demo"}, false},
		{"no flags → scoped", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFlagSet()
			opts := registerAuthFlags(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			if got := allProjectsMode(opts); got != tc.want {
				t.Fatalf("allProjectsMode = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGlobalAdminFlagPopulatesAuthOptions(t *testing.T) {
	fs := newFlagSet()
	opts := registerAuthFlags(fs)
	if err := fs.Parse([]string{"--global-admin"}); err != nil {
		t.Fatal(err)
	}
	if !opts.GlobalAdmin {
		t.Fatal("--global-admin did not populate auth options")
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
