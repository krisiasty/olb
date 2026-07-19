package osclient

import (
	"os"
	"testing"
)

func TestProjectFilterDoesNotOverrideAuthenticationProject(t *testing.T) {
	t.Setenv("OS_PROJECT_NAME", "authentication-project")
	Options{Project: "presentation-project"}.applyToEnv()
	if got := os.Getenv("OS_PROJECT_NAME"); got != "authentication-project" {
		t.Fatalf("OS_PROJECT_NAME = %q, want authentication-project", got)
	}
}

func TestOSProjectNameStillOverridesAuthenticationProject(t *testing.T) {
	t.Setenv("OS_PROJECT_NAME", "environment-project")
	Options{ProjectName: "cli-authentication-project"}.applyToEnv()
	if got := os.Getenv("OS_PROJECT_NAME"); got != "cli-authentication-project" {
		t.Fatalf("OS_PROJECT_NAME = %q, want cli-authentication-project", got)
	}
}
