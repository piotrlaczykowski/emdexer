package fuzz

import (
	"strings"
	"testing"
)

// Simplified version of the auth logic from gateway/main.go for fuzzing/unit testing.
// In a real scenario, this would test the actual middleware function.
func namespaceValidationLogic(allowedNamespaces []string, requestedNamespace string) (bool, string) {
	if requestedNamespace == "" {
		requestedNamespace = "default"
	}

	isAllowed := false
	for _, ns := range allowedNamespaces {
		if ns == "*" || ns == requestedNamespace {
			isAllowed = true
			break
		}
	}
	return isAllowed, requestedNamespace
}

func FuzzNamespaceValidation(f *testing.F) {
	f.Add("alpha,beta", "alpha")
	f.Add("*", "any")
	f.Add("prod", "../admin")
	f.Add("default", "")

	f.Fuzz(func(t *testing.T, allowedCSV string, requested string) {
		allowed := strings.Split(allowedCSV, ",")
		
		isAllowed, finalNamespace := namespaceValidationLogic(allowed, requested)
		
		if isAllowed {
			// If allowed, ensure it's either in the list or the list has a wildcard
			found := false
			for _, ns := range allowed {
				if ns == "*" || ns == finalNamespace {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Allowed namespace %q not found in allowed list %v", finalNamespace, allowed)
			}
		}
		
		// Ensure it never returns empty if input wasn't empty (or if it defaults to 'default')
		if finalNamespace == "" {
			t.Errorf("Resulting namespace should not be empty")
		}
	})
}
