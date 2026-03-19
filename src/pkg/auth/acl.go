package auth

import (
	"encoding/json"
	"fmt"
)

// GroupACL maps OIDC groups to authorized Emdexer namespaces.
type GroupACL struct {
	Mapping map[string][]string // group name -> []namespace
}

// NewGroupACL parses a JSON string mapping groups to namespace lists.
// Example: {"hr-admins": ["hr", "hiring"], "engineers": ["*"]}
func NewGroupACL(jsonStr string) (*GroupACL, error) {
	var mapping map[string][]string
	if err := json.Unmarshal([]byte(jsonStr), &mapping); err != nil {
		return nil, fmt.Errorf("invalid group ACL JSON: %w", err)
	}
	return &GroupACL{Mapping: mapping}, nil
}

// ResolveNamespaces returns the union of authorized namespaces for the given groups.
// If any group grants ["*"], the result is ["*"] (wildcard access).
func (a *GroupACL) ResolveNamespaces(groups []string) []string {
	if a == nil || a.Mapping == nil {
		return nil
	}

	seen := make(map[string]bool)
	var result []string

	for _, group := range groups {
		namespaces, ok := a.Mapping[group]
		if !ok {
			continue
		}
		for _, ns := range namespaces {
			if ns == "*" {
				return []string{"*"}
			}
			if !seen[ns] {
				seen[ns] = true
				result = append(result, ns)
			}
		}
	}
	return result
}
