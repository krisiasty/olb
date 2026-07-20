package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/krisiasty/olb/internal/osclient"
)

// newFlagSet builds the flag set with a usage banner describing the v1 surface.
func newFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("olb", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "olb — interactive TUI for OpenStack Octavia load balancers")
		fmt.Fprintln(os.Stderr, "\nUsage: olb [flags]\n\nWith no arguments, lists the load balancers in the current project.")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\nAuthentication mirrors python-openstackclient (OS_* env vars, clouds.yaml,")
		fmt.Fprintln(os.Stderr, "and the flags below). Precedence: CLI flags > environment > clouds.yaml.")
	}
	return fs
}

// registerAuthFlags wires the openstackclient-style auth flags into fs and
// returns the Options they populate. Only flags the user actually sets take
// effect; the rest fall through to env / clouds.yaml.
func registerAuthFlags(fs *flag.FlagSet) *osclient.Options {
	o := &osclient.Options{}
	fs.StringVar(&o.Cloud, "os-cloud", "", "clouds.yaml entry to use (or $OS_CLOUD)")
	fs.StringVar(&o.Region, "os-region-name", "", "region to use (or $OS_REGION_NAME)")
	fs.StringVar(&o.Project, "project", "", "initial project selection (name or ID)")
	fs.BoolVar(&o.GlobalAdmin, "global-admin", false, "treat credentials as a global administrator; retain their scope when selecting projects")

	fs.StringVar(&o.AuthURL, "os-auth-url", "", "Keystone auth URL (or $OS_AUTH_URL)")
	fs.StringVar(&o.Username, "os-username", "", "username (or $OS_USERNAME)")
	fs.StringVar(&o.Password, "os-password", "", "password (or $OS_PASSWORD)")
	fs.StringVar(&o.UserDomainName, "os-user-domain-name", "", "user domain name (or $OS_USER_DOMAIN_NAME)")
	fs.StringVar(&o.ProjectName, "os-project-name", "", "project name (or $OS_PROJECT_NAME)")
	fs.StringVar(&o.ProjectID, "os-project-id", "", "project id (or $OS_PROJECT_ID)")
	fs.StringVar(&o.ProjectDomainName, "os-project-domain-name", "", "project domain name (or $OS_PROJECT_DOMAIN_NAME)")
	fs.StringVar(&o.Token, "os-token", "", "pre-issued token (or $OS_TOKEN)")
	fs.StringVar(&o.ApplicationCredentialID, "os-application-credential-id", "", "application credential id (or $OS_APPLICATION_CREDENTIAL_ID)")
	fs.StringVar(&o.ApplicationCredentialName, "os-application-credential-name", "", "application credential name (or $OS_APPLICATION_CREDENTIAL_NAME)")
	fs.StringVar(&o.ApplicationCredentialSecret, "os-application-credential-secret", "", "application credential secret (or $OS_APPLICATION_CREDENTIAL_SECRET)")
	return o
}
