// Package policy decides which Google identities may use ClearSky.
package google

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

var domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

// Policy admits verified Google emails of the university's domains. With
// RequireWorkspace, the account must also belong to the university's Google
// Workspace (the hd claim); a personal Google account registered with a
// university address is then rejected.
type Policy struct {
	Domains          []string
	RequireWorkspace bool
}

// FromEnvironment reads GOOGLE_ALLOWED_DOMAINS (comma separated, e.g.
// "dept.uni.gr,uni.gr") and GOOGLE_REQUIRE_WORKSPACE (default true).
func FromEnvironment() (Policy, error) {
	var domains []string
	for _, raw := range strings.Split(os.Getenv("GOOGLE_ALLOWED_DOMAINS"), ",") {
		domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
		if domain == "" {
			continue
		}
		if !domainPattern.MatchString(domain) {
			return Policy{}, fmt.Errorf("GOOGLE_ALLOWED_DOMAINS contains an invalid domain %q", domain)
		}
		domains = append(domains, domain)
	}
	if len(domains) == 0 {
		return Policy{}, errors.New("GOOGLE_ALLOWED_DOMAINS must list at least one university domain")
	}
	require := true
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GOOGLE_REQUIRE_WORKSPACE"))) {
	case "", "true":
	case "false":
		require = false
	default:
		return Policy{}, errors.New("GOOGLE_REQUIRE_WORKSPACE must be true or false")
	}
	return Policy{Domains: domains, RequireWorkspace: require}, nil
}

// Allows reports whether a Google-verified email may sign in. The email's
// domain must match an allowed domain exactly (subdomains are not implied).
func (p Policy) Allows(email, hostedDomain string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at <= 0 || !slices.Contains(p.Domains, email[at+1:]) {
		return false
	}
	if !p.RequireWorkspace {
		return true
	}
	return slices.Contains(p.Domains, strings.ToLower(strings.TrimSpace(hostedDomain)))
}

// HostedDomainHint lets Google preselect the university account when there
// is exactly one domain. It is a convenience only; Allows is the check.
func (p Policy) HostedDomainHint() string {
	if len(p.Domains) == 1 {
		return p.Domains[0]
	}
	return ""
}
