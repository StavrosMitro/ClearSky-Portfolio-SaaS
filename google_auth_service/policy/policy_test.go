package policy

import "testing"

func TestFromEnvironment(t *testing.T) {
	t.Setenv("GOOGLE_REQUIRE_WORKSPACE", "")
	for value, wantErr := range map[string]bool{
		"":                          true,
		" , ":                       true,
		"not a domain":              true,
		"@Dept.Uni.GR, uni.gr":      false,
		"dept.uni.gr,evil..example": true,
	} {
		t.Setenv("GOOGLE_ALLOWED_DOMAINS", value)
		p, err := FromEnvironment()
		if (err != nil) != wantErr {
			t.Fatalf("%q: err = %v, wantErr %v", value, err, wantErr)
		}
		if !wantErr && (len(p.Domains) != 2 || p.Domains[0] != "dept.uni.gr" || !p.RequireWorkspace) {
			t.Fatalf("%q: policy = %+v", value, p)
		}
	}
	t.Setenv("GOOGLE_ALLOWED_DOMAINS", "uni.gr")
	t.Setenv("GOOGLE_REQUIRE_WORKSPACE", "maybe")
	if _, err := FromEnvironment(); err == nil {
		t.Fatal("an invalid GOOGLE_REQUIRE_WORKSPACE must be rejected")
	}
}

func TestAllows(t *testing.T) {
	workspace := Policy{Domains: []string{"dept.uni.gr"}, RequireWorkspace: true}
	tests := []struct {
		name, email, hd string
		want            bool
	}{
		{"workspace student", "alice@dept.uni.gr", "dept.uni.gr", true},
		{"case differences", "Alice@Dept.Uni.GR", "DEPT.UNI.GR", true},
		{"personal gmail", "alice@gmail.com", "", false},
		{"personal account with university address", "alice@dept.uni.gr", "", false},
		{"other workspace", "alice@dept.uni.gr", "evil.example", false},
		{"subdomain is not implied", "alice@x.dept.uni.gr", "x.dept.uni.gr", false},
		{"lookalike suffix", "alice@evildept.uni.gr", "evildept.uni.gr", false},
		{"missing local part", "@dept.uni.gr", "dept.uni.gr", false},
	}
	for _, tt := range tests {
		if got := workspace.Allows(tt.email, tt.hd); got != tt.want {
			t.Errorf("%s: Allows(%q, %q) = %v, want %v", tt.name, tt.email, tt.hd, got, tt.want)
		}
	}

	domainOnly := Policy{Domains: []string{"dept.uni.gr"}}
	if !domainOnly.Allows("alice@dept.uni.gr", "") || domainOnly.Allows("alice@gmail.com", "") {
		t.Fatal("without the Workspace requirement only the email domain is checked")
	}
}
