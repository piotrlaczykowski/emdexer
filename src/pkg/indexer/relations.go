package indexer

import (
	"bufio"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// Relation represents one structural link extracted from a file during indexing.
// It is serialised to JSON and stored in the Qdrant point payload under the "relations" key.
// Only chunk 0 of a file carries relations; all other chunks omit the field.
type Relation struct {
	Type   string `json:"type"`
	Target string `json:"target,omitempty"` // imports, links_to
	Name   string `json:"name,omitempty"`   // defines
}

// ExtractRelations returns the structural relations for a file given its path and full text.
// Supported relation types by language:
//
//   Go / Python    — imports (import paths), defines (exported names)
//   JS / TS        — imports (require/from paths), defines (exported identifiers)
//   C / C++        — imports (#include paths)
//   Markdown / RST — links_to (relative link targets, http(s) links excluded)
//
// Returns nil for unsupported file extensions so callers can skip the payload field.
func ExtractRelations(path, text string) []Relation {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return extractGoRelations(text)
	case ".py":
		return extractPythonRelations(text)
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return extractJSRelations(text)
	case ".c", ".cpp", ".cc", ".cxx", ".h", ".hpp":
		return extractCRelations(text)
	case ".md", ".mdx", ".rst":
		return extractMarkdownLinks(text)
	}
	return nil
}

// RelationsToJSON serialises relations to a compact JSON string for Qdrant payload storage.
// Returns "" when the slice is empty so callers can omit the payload key entirely.
func RelationsToJSON(rels []Relation) string {
	if len(rels) == 0 {
		return ""
	}
	b, _ := json.Marshal(rels)
	return string(b)
}

// ── Go ────────────────────────────────────────────────────────────────────────

var (
	reGoImportPath   = regexp.MustCompile(`"([^"]+)"`)
	reGoFuncExported = regexp.MustCompile(`^func\s+(?:\(\w[\w*]*\s+[\w*]+\)\s+)?([A-Z]\w*)\s*[(\[]`)
	reGoTypeExported = regexp.MustCompile(`^type\s+([A-Z]\w*)\s+`)
)

func extractGoRelations(text string) []Relation {
	var rels []Relation
	scanner := bufio.NewScanner(strings.NewReader(text))

	inImport := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// track import block boundaries
		if strings.HasPrefix(trimmed, "import (") {
			inImport = true
			continue
		}
		if inImport && trimmed == ")" {
			inImport = false
			continue
		}
		// single-line: import "path"  or  import alias "path"
		if !inImport && strings.HasPrefix(trimmed, "import ") {
			if m := reGoImportPath.FindStringSubmatch(trimmed); m != nil {
				rels = append(rels, Relation{Type: "imports", Target: m[1]})
			}
			continue
		}
		if inImport {
			if m := reGoImportPath.FindStringSubmatch(trimmed); m != nil {
				rels = append(rels, Relation{Type: "imports", Target: m[1]})
			}
			continue
		}

		// exported top-level declarations
		if m := reGoFuncExported.FindStringSubmatch(trimmed); m != nil {
			rels = append(rels, Relation{Type: "defines", Name: m[1]})
		} else if m := reGoTypeExported.FindStringSubmatch(trimmed); m != nil {
			rels = append(rels, Relation{Type: "defines", Name: m[1]})
		}
	}
	return rels
}

// ── Python ────────────────────────────────────────────────────────────────────

var (
	rePyImport     = regexp.MustCompile(`^import\s+([\w.]+)`)
	rePyFromImport = regexp.MustCompile(`^from\s+([\w.]+)\s+import`)
	rePyDef        = regexp.MustCompile(`^(?:(?:async\s+)?def|class)\s+([A-Za-z_]\w*)\s*[:(]`)
)

func extractPythonRelations(text string) []Relation {
	var rels []Relation
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case rePyImport.MatchString(line):
			m := rePyImport.FindStringSubmatch(line)
			rels = append(rels, Relation{Type: "imports", Target: m[1]})
		case rePyFromImport.MatchString(line):
			m := rePyFromImport.FindStringSubmatch(line)
			rels = append(rels, Relation{Type: "imports", Target: m[1]})
		default:
			if m := rePyDef.FindStringSubmatch(line); m != nil {
				rels = append(rels, Relation{Type: "defines", Name: m[1]})
			}
		}
	}
	return rels
}

// ── JavaScript / TypeScript ───────────────────────────────────────────────────

var (
	reJSImport = regexp.MustCompile(`(?:from|require)\s*\(?['"]([^'"]+)['"]`)
	reJSDef    = regexp.MustCompile(`^(?:export\s+(?:default\s+)?)?(?:function\*?|class|(?:const|let|var)\s+([A-Za-z_$][\w$]*))\s+([A-Za-z_$][\w$]*)`)
)

func extractJSRelations(text string) []Relation {
	var rels []Relation
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())
		if m := reJSImport.FindStringSubmatch(trimmed); m != nil {
			rels = append(rels, Relation{Type: "imports", Target: m[1]})
		}
		if m := reJSDef.FindStringSubmatch(trimmed); m != nil {
			// prefer the second capture group (the name after the keyword)
			name := m[2]
			if name == "" {
				name = m[1]
			}
			if name != "" {
				rels = append(rels, Relation{Type: "defines", Name: name})
			}
		}
	}
	return rels
}

// ── C / C++ ───────────────────────────────────────────────────────────────────

var reCInclude = regexp.MustCompile(`^\s*#\s*include\s*["<]([^">]+)[">]`)

func extractCRelations(text string) []Relation {
	var rels []Relation
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		if m := reCInclude.FindStringSubmatch(scanner.Text()); m != nil {
			rels = append(rels, Relation{Type: "imports", Target: m[1]})
		}
	}
	return rels
}

// ── Markdown / RST ────────────────────────────────────────────────────────────

var reMdLink = regexp.MustCompile(`\[[^\]]*\]\(([^)#? ]+)\)`)

func extractMarkdownLinks(text string) []Relation {
	var rels []Relation
	for _, m := range reMdLink.FindAllStringSubmatch(text, -1) {
		target := strings.TrimSpace(m[1])
		if target == "" || strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
			continue
		}
		rels = append(rels, Relation{Type: "links_to", Target: target})
	}
	return rels
}
