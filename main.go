// Command olb is an interactive TUI for inspecting OpenStack Octavia load
// balancers (Amphora and OVN providers).
//
// v1 is interactive-only; a non-interactive/scriptable mode is deferred, so the
// flag surface is small and flat and the standard-library flag package covers
// it. Authentication mirrors python-openstackclient: OS_* env vars, clouds.yaml
// (--os-cloud), and CLI flags, with precedence CLI > env > clouds.yaml.
package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"

	"github.com/krisiasty/olb/internal/osclient"
	"github.com/krisiasty/olb/internal/telemetry"
	"github.com/krisiasty/olb/internal/tui"
	"github.com/krisiasty/olb/internal/version"
)

// thirdPartyNotices is the aggregated dependency attribution, embedded so it
// travels inside the binary regardless of how it is distributed. Exposed via
// `olb --licenses`.
//
//go:embed THIRD_PARTY_NOTICES
var thirdPartyNotices string

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "olb: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) (runErr error) {
	fs := newFlagSet()
	var (
		showVersion  = fs.Bool("version", false, "print version and exit")
		showLicenses = fs.Bool("licenses", false, "print third-party license notices and exit")
		printMode    = fs.Bool("print", false, "copy actions show the value on screen for manual copy instead of emitting OSC 52")
		apiLogPath   = fs.String("api-log", "", "append sanitized API request/response metadata as JSON Lines to this file")
		apiLogBodies = fs.Bool("api-log-bodies", false, "include sanitized, size-limited JSON bodies in --api-log (requires --api-log)")
	)
	opts := registerAuthFlags(fs)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(version.String())
		return nil
	}
	if *showLicenses {
		fmt.Print(thirdPartyNotices)
		return nil
	}
	if *apiLogBodies && *apiLogPath == "" {
		return errors.New("--api-log-bodies requires --api-log PATH")
	}
	// A global administrator with no explicit --project starts in the all-projects
	// view; a concrete --project scopes to that project while retaining the global
	// credential.
	allProjects := allProjectsMode(opts)

	var apiLogger *telemetry.APILogger
	if *apiLogPath != "" {
		var err error
		apiLogger, err = telemetry.OpenAPILogger(*apiLogPath, telemetry.APILogOptions{IncludeBodies: *apiLogBodies})
		if err != nil {
			return fmt.Errorf("opening API log %q: %w", *apiLogPath, err)
		}
		defer func() {
			runErr = errors.Join(runErr, apiLogger.Close())
		}()
	}

	ctx := context.Background()
	clients, err := osclient.Authenticate(ctx, *opts, osclient.WithAPILogger(apiLogger))
	if err != nil {
		return err
	}
	if allProjects {
		if err := clients.EnterAllProjects(ctx); err != nil {
			return err
		}
	} else if opts.Project != "" {
		if err := clients.SelectProject(ctx, opts.Project); err != nil {
			return err
		}
	}

	return tui.Run(clients, tui.Config{
		PrintMode:   *printMode,
		AllProjects: allProjects,
		Stdout:      os.Stdout,
	})
}
