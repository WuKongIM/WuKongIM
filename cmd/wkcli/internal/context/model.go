package contextcmd

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var contextNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Context is a named set of WuKongIM server API addresses.
type Context struct {
	// Name identifies the context and is also used as its file name.
	Name string `json:"name"`
	// Description is optional human-readable context notes.
	Description string `json:"description,omitempty"`
	// Servers contains one or more WuKongIM HTTP API base addresses.
	Servers []string `json:"servers"`
}

func parseServerValues(values []string) []string {
	var servers []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			server := strings.TrimSpace(part)
			if server != "" {
				servers = append(servers, server)
			}
		}
	}
	return servers
}

func validateContext(ctx Context) error {
	if err := validateContextName(ctx.Name); err != nil {
		return err
	}
	if len(ctx.Servers) == 0 {
		return fmt.Errorf("--server is required")
	}
	seen := make(map[string]struct{}, len(ctx.Servers))
	for _, server := range ctx.Servers {
		if err := validateServer(server); err != nil {
			return err
		}
		if _, ok := seen[server]; ok {
			return fmt.Errorf("duplicate server %q", server)
		}
		seen[server] = struct{}{}
	}
	return nil
}

func validateContextName(name string) error {
	if !contextNamePattern.MatchString(name) {
		return fmt.Errorf("context name must match %s", contextNamePattern.String())
	}
	return nil
}

func validateServer(server string) error {
	parsed, err := url.Parse(server)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("server %q must be an absolute http or https URL", server)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("server %q must use http or https", server)
	}
	return nil
}
