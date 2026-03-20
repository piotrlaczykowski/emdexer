package auth

import (
	"testing"
)

func TestGroupACL_BasicMapping(t *testing.T) {
	acl, err := NewGroupACL(`{"hr": ["hr-docs", "hiring"]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ns := acl.ResolveNamespaces([]string{"hr"})
	if len(ns) != 2 {
		t.Fatalf("expected 2 namespaces, got %d: %v", len(ns), ns)
	}

	expected := map[string]bool{"hr-docs": true, "hiring": true}
	for _, n := range ns {
		if !expected[n] {
			t.Errorf("unexpected namespace %q", n)
		}
	}
}

func TestGroupACL_MultipleGroups(t *testing.T) {
	acl, err := NewGroupACL(`{"hr": ["hr-docs", "shared"], "eng": ["eng-docs", "shared"]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ns := acl.ResolveNamespaces([]string{"hr", "eng"})

	// Should be deduplicated: hr-docs, shared, eng-docs (3 unique)
	seen := make(map[string]int)
	for _, n := range ns {
		seen[n]++
	}
	if seen["shared"] != 1 {
		t.Errorf("expected 'shared' to appear once, appeared %d times", seen["shared"])
	}
	if len(ns) != 3 {
		t.Fatalf("expected 3 unique namespaces, got %d: %v", len(ns), ns)
	}
}

func TestGroupACL_Wildcard(t *testing.T) {
	acl, err := NewGroupACL(`{"admins": ["*"], "hr": ["hr-docs"]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ns := acl.ResolveNamespaces([]string{"admins"})
	if len(ns) != 1 || ns[0] != "*" {
		t.Fatalf("expected [*], got %v", ns)
	}

	// Even with multiple groups, wildcard should short-circuit
	ns = acl.ResolveNamespaces([]string{"hr", "admins"})
	if len(ns) != 1 || ns[0] != "*" {
		t.Fatalf("expected [*] when any group has wildcard, got %v", ns)
	}
}

func TestGroupACL_NoMatch(t *testing.T) {
	acl, err := NewGroupACL(`{"hr": ["hr-docs"]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ns := acl.ResolveNamespaces([]string{"eng"})
	if len(ns) != 0 {
		t.Fatalf("expected empty namespaces for non-matching group, got %v", ns)
	}
}

func TestNewGroupACL_InvalidJSON(t *testing.T) {
	_, err := NewGroupACL(`{invalid json}`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestGroupACL_NilACL(t *testing.T) {
	var acl *GroupACL
	ns := acl.ResolveNamespaces([]string{"hr"})
	if ns != nil {
		t.Fatalf("expected nil for nil ACL, got %v", ns)
	}
}
