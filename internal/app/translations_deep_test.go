package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDeepTranslationAudit(t *testing.T) {
	usedKeys := make(map[string][]string)

	err := filepath.Walk("../..", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.Contains(path, "vendor") || strings.Contains(path, "translations_") {
			return nil
		}

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}

		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			var funcName string
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				funcName = fn.Name
			case *ast.SelectorExpr:
				funcName = fn.Sel.Name
			}

			if funcName == "Tr" || funcName == "Translate" || funcName == "tr" {
				if len(call.Args) > 0 {
					argIdx := 0
					if funcName == "Translate" && len(call.Args) >= 2 {
						argIdx = 1
					}
					if lit, ok := call.Args[argIdx].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						key := strings.Trim(lit.Value, "\"")
						usedKeys[key] = append(usedKeys[key], path)
					}
				}
			}
			return true
		})
		return nil
	})

	if err != nil {
		t.Fatalf("Walk error: %v", err)
	}

	enMap := translations["en"]

	// 1. Check if any code uses a key not in translations["en"]
	missingInEn := 0
	for key, files := range usedKeys {
		if _, ok := enMap[key]; !ok {
			t.Errorf("KEY USED IN CODE BUT MISSING IN translations['en']: %q (used in %v)", key, files)
			missingInEn++
		}
	}
	t.Logf("Total unique keys used in code: %d, Missing in EN: %d", len(usedKeys), missingInEn)

	// 2. Check verb count matches
	reVerb := regexp.MustCompile(`%[+\-# 0]*(\*|\d+)?(\.\*|\.\d+)?[vTtbcdoqxXUeEfFgGsp]`)

	for lang, langMap := range translations {
		if lang == "en" {
			continue
		}
		missingCount := 0
		verbMismatchCount := 0
		for key, enText := range enMap {
			langText, exists := langMap[key]
			if !exists {
				t.Errorf("[%s] Missing key: %q", lang, key)
				missingCount++
				continue
			}

			enVerbs := reVerb.FindAllString(enText, -1)
			langVerbs := reVerb.FindAllString(langText, -1)

			if len(enVerbs) != len(langVerbs) {
				t.Errorf("[%s] Verb count mismatch for %q:\n  EN (%d verbs): %q -> %v\n  %s (%d verbs): %q -> %v",
					lang, key, len(enVerbs), enText, enVerbs, strings.ToUpper(lang), len(langVerbs), langText, langVerbs)
				verbMismatchCount++
			}
		}
		t.Logf("[%s] Missing keys: %d, Verb mismatches: %d (out of %d EN keys)", lang, missingCount, verbMismatchCount, len(enMap))
	}
}
