//go:build enable_linters

// Package canonicalnames is a build-tagged lint that flags new exported
// type declarations whose names match the parallel-shape pattern
// forbidden by ADR 0001 (../../../../platform/docs/adr/0001-canonical-domain-model.md).
//
// Scope: internal/defs, internal/api, internal/conf — the wire-facing
// and config-translation boundaries. Pre-existing matches are
// grandfathered via grandfathered.txt; new matches fail the lint.
package canonicalnames

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const repoPath = "../../.."

// inScopePackages are repo-relative directories of the packages whose
// exported types are subject to the canonical-name rule.
var inScopePackages = []string{
	"internal/defs",
	"internal/api",
	"internal/conf",
}

// suffixPattern matches exported types whose name ends in DTO / Info /
// View / Record / Model — the parallel-shape pattern forbidden by
// ADR 0001.
var suffixPattern = regexp.MustCompile(`^[A-Z]\w*(DTO|Info|View|Record|Model)$`)

func loadGrandfathered(t *testing.T) map[string]struct{} {
	t.Helper()
	f, err := os.Open("grandfathered.txt")
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]struct{}{}
		}
		t.Fatalf("opening grandfathered.txt: %v", err)
	}
	defer f.Close()

	m := map[string]struct{}{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m[line] = struct{}{}
	}
	require.NoError(t, scanner.Err())
	return m
}

func TestCanonicalNames(t *testing.T) {
	grandfathered := loadGrandfathered(t)
	var violations []string

	for _, pkgRel := range inScopePackages {
		pkgDir := filepath.Join(repoPath, pkgRel)
		pkgName := filepath.Base(pkgDir)

		err := filepath.WalkDir(pkgDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}

			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					name := ts.Name.Name
					if !ts.Name.IsExported() {
						continue
					}
					if !suffixPattern.MatchString(name) {
						continue
					}
					qualified := pkgName + "." + name
					if _, ok := grandfathered[qualified]; ok {
						continue
					}
					rel, _ := filepath.Rel(repoPath, path)
					violations = append(violations, qualified+" ("+rel+")")
				}
			}
			return nil
		})
		require.NoError(t, err)
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf(
			"found %d new canonical-name violation(s) — see ADR 0001:\n"+
				"  %s\n\n"+
				"Either rename to use a canonical entity name from\n"+
				"../../../platform/docs/domain-model.md, or (if a rename is not yet\n"+
				"possible) add the qualified name to grandfathered.txt with\n"+
				"a one-line justification.",
			len(violations), strings.Join(violations, "\n  "),
		)
	}
}
