package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// localizationLangs is the authoritative list of supported languages.
//
// `en` is the fallback: it is the only dictionary allowed to have holes,
// because translateByLanguage falls back to it for every other language, for an
// unknown language, and for a key missing from a language. A language with
// fewer keys than the others is a localization bug even when no test fails.
var localizationLangs = []string{"en", "it", "es", "de", "zh", "uk"}

// translationsSourceFile is the single file that owns the dictionary.
const translationsSourceFile = "translations.go"

// rawTranslations parses internal/app/translations.go and returns the dictionary
// exactly as it is written in the source.
//
// It deliberately does NOT read the in-memory `translations` variable: the
// previous version of these tests called ensureTranslationCoverage(), which
// filled every hole with the English value before any assertion could look at
// the map, so the coverage tests were tautological (delete a key from `it` and
// the suite still reported "Coverage for it: 100.0%"). The runtime filler is
// gone now — see translations_runtime.go — so source and memory agree, and
// TestTranslationsRawMatchesMemory keeps them honest.
//
// It also fails on duplicate keys inside a block, which the compiler would
// reject anyway but which is worth naming explicitly.
func rawTranslations(t *testing.T) map[string]map[string]string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, translationsSourceFile, nil, 0)
	if err != nil {
		t.Fatalf("cannot parse %s: %v", translationsSourceFile, err)
	}

	var root *ast.CompositeLit
	ast.Inspect(file, func(n ast.Node) bool {
		gd, ok := n.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			return true
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "translations" {
				continue
			}
			if cl, ok := vs.Values[0].(*ast.CompositeLit); ok {
				root = cl
			}
		}
		return true
	})
	if root == nil {
		t.Fatalf("var translations = map[string]map[string]string{...} not found in %s", translationsSourceFile)
	}

	out := make(map[string]map[string]string, len(root.Elts))
	for _, langElt := range root.Elts {
		kv, ok := langElt.(*ast.KeyValueExpr)
		if !ok {
			t.Fatalf("unexpected element in translations literal: %T", langElt)
		}
		lang, err := strconv.Unquote(kv.Key.(*ast.BasicLit).Value)
		if err != nil {
			t.Fatalf("unparseable language key: %v", err)
		}
		cl, ok := kv.Value.(*ast.CompositeLit)
		if !ok {
			t.Fatalf("language %q: expected a nested map literal, got %T", lang, kv.Value)
		}
		dict := make(map[string]string, len(cl.Elts))
		for _, entry := range cl.Elts {
			ekv, ok := entry.(*ast.KeyValueExpr)
			if !ok {
				t.Fatalf("[%s] unexpected element: %T", lang, entry)
			}
			key, err := strconv.Unquote(ekv.Key.(*ast.BasicLit).Value)
			if err != nil {
				t.Fatalf("[%s] unparseable key: %v", lang, err)
			}
			val, err := strconv.Unquote(ekv.Value.(*ast.BasicLit).Value)
			if err != nil {
				t.Fatalf("[%s] key %q: unparseable value: %v", lang, key, err)
			}
			if prev, dup := dict[key]; dup {
				t.Fatalf("[%s] duplicate key %q: %q and %q", lang, key, prev, val)
			}
			dict[key] = val
		}
		out[lang] = dict
	}
	return out
}

// TestTranslationsRawMatchesMemory makes the guarantee explicit: nothing may
// mutate `translations` at init time. If a future init() adds a filler again,
// this test fails here instead of silently making the coverage tests vacuous.
func TestTranslationsRawMatchesMemory(t *testing.T) {
	raw := rawTranslations(t)

	for _, lang := range localizationLangs {
		rawDict, ok := raw[lang]
		if !ok {
			t.Errorf("language %q is missing from the source file", lang)
			continue
		}
		memDict, ok := translations[lang]
		if !ok {
			t.Errorf("language %q is missing from the in-memory map", lang)
			continue
		}
		if len(rawDict) != len(memDict) {
			t.Errorf("language %q: source has %d keys, in-memory map has %d; "+
				"the in-memory map is being mutated at runtime (see translations_runtime.go)",
				lang, len(rawDict), len(memDict))
		}
		for key, val := range rawDict {
			if got, ok := memDict[key]; !ok {
				t.Errorf("language %q: key %q is in the source but not in the in-memory map", lang, key)
			} else if got != val {
				t.Errorf("language %q: key %q was rewritten in memory: source %q, memory %q", lang, key, val, got)
			}
		}
	}

	for lang := range translations {
		if _, ok := raw[lang]; !ok {
			t.Errorf("language %q exists in memory but not in the source file", lang)
		}
	}
}

// TestTranslations_TotalCoverage reports the per-language coverage of the
// dictionary read from the source file, and fails if any language is below 100%.
func TestTranslations_TotalCoverage(t *testing.T) {
	raw := rawTranslations(t)

	en, ok := raw["en"]
	if !ok {
		t.Fatalf("english translations missing")
	}
	totalKeys := len(en)

	allPassed := true
	for _, lang := range localizationLangs {
		if lang == "en" {
			continue
		}

		dict, ok := raw[lang]
		if !ok {
			t.Errorf("language %q is missing entirely", lang)
			allPassed = false
			continue
		}

		missingKeys := make([]string, 0)
		for key := range en {
			if _, ok := dict[key]; !ok {
				missingKeys = append(missingKeys, key)
			}
		}
		sort.Strings(missingKeys)
		for _, key := range missingKeys {
			t.Errorf("Language '%s' missing key: %s", lang, key)
		}

		presentKeys := totalKeys - len(missingKeys)
		percentage := float64(presentKeys) / float64(totalKeys) * 100.0
		t.Logf("Coverage for %s: %.1f%% (%d/%d keys)", lang, percentage, presentKeys, totalKeys)

		if len(missingKeys) > 0 {
			allPassed = false
		}
	}

	if !allPassed {
		t.Fatalf("Translation coverage check failed. Some languages are missing keys.")
	}
}

// TestTranslationsNoUnknownLanguages guards against a language block being
// added to the source without being added to localizationLangs: such a block
// would never be covered by TestTranslations_TotalCoverage.
func TestTranslationsNoUnknownLanguages(t *testing.T) {
	raw := rawTranslations(t)

	known := make(map[string]bool, len(localizationLangs))
	for _, lang := range localizationLangs {
		known[lang] = true
	}
	for lang := range raw {
		if !known[lang] {
			t.Errorf("language %q is present in %s but not in localizationLangs; "+
				"add it there or the coverage tests will silently skip it",
				lang, translationsSourceFile)
		}
	}
}

// pendingKeyAdoption lists the keys created during audit phase 2 whose call
// sites still contain hardcoded English text in files owned by other lanes.
//
// A key listed here is NOT exempt from the coverage, placeholder and marker
// checks: it must exist in all six languages with the correct verbs and
// balanced Markdown. It is only exempt from TestTranslationsNoUnusedKeys,
// because nobody calls it yet. Delete an entry as soon as its lane lands;
// TestTranslationsPendingAdoptionIsDefined fails if an entry no longer exists,
// so the list cannot rot into hiding real dead keys.
var pendingKeyAdoption = map[string]string{
	"net_down_reason_gateway":    "internal/app/monitor_network.go:51",
	"net_down_reason_no_targets": "internal/app/monitor_network.go:62",
	"net_down_reason_dns":        "internal/app/monitor_network.go:67",
	"adblock_err_create_req":     "internal/app/handlers_adblock_callback.go:45",
	"adblock_err_contact":        "internal/app/handlers_adblock_callback.go:56",
	"docker_container_not_found": "internal/app/docker.go:552",
	"prune_err_title":            "internal/app/monitor_docker.go:158",
	"prune_err_timeout":          "internal/app/monitor_docker.go:156",
	"prune_ok_title":             "internal/app/monitor_docker.go:170",
	"prune_done_event":           "internal/app/monitor_docker.go:171",
	"prune_day_invalid":          "internal/app/handlers_callback_routes.go prune_day_",
	"fmt_period_seconds":         "internal/format/format.go FormatPeriod",
	"fmt_period_minute":          "internal/format/format.go FormatPeriod",
	"fmt_period_minutes":         "internal/format/format.go FormatPeriod",
	"fmt_period_hour":            "internal/format/format.go FormatPeriod",
	"fmt_period_hours":           "internal/format/format.go FormatPeriod",
	"fmt_period_day":             "internal/format/format.go FormatPeriod",
	"fmt_period_days":            "internal/format/format.go FormatPeriod",
	// Same text as the reportTruncatedNotice constant at reports_schedule.go:34.
	"report_truncated": "internal/app/reports_schedule.go:34 reportTruncatedNotice",
	// 3rd %s of the prompt, after the container name and the logs.
	"docker_ai_prompt": "internal/app/docker.go — pass the language name in English",
}

// TestTranslationsPendingAdoptionIsDefined keeps pendingKeyAdoption honest: an
// entry that names a key which no longer exists is a stale bookkeeping entry
// and is itself a defect.
func TestTranslationsPendingAdoptionIsDefined(t *testing.T) {
	raw := rawTranslations(t)

	en := raw["en"]
	for key, owner := range pendingKeyAdoption {
		if _, ok := en[key]; !ok {
			t.Errorf("pendingKeyAdoption lists %q (%s) but that key is not defined in en", key, owner)
		}
	}
}

// TestTranslationsNoUnusedKeys fails on keys defined in every language but
// referenced from nowhere. A dead key is maintenance that can never take
// effect, and it hides the fact that the string it was written for is still
// hardcoded somewhere.
//
// Detection is by string literal outside the dictionary: any literal whose
// value equals a defined key counts as a use, except literals that are keys of
// a composite literal (JSON config field names such as "enabled" live there)
// and struct tags. Composite-literal keys and tags are skipped because they
// live in the AST as the Key of a KeyValueExpr or inside a Field.Tag, never as
// an argument.
//
// Keys built by concatenation are recognised too: languageSetKey returns
// "lang_set_" + lang, so only `lang_set_en` appears verbatim. A literal that is
// one operand of a `+` and looks like a key namespace (reKeyNamespacePrefix)
// marks every defined key starting with it as used. Requiring the namespace
// shape matters: a bare "_" or "-" operand would otherwise mask the whole
// dictionary.
func TestTranslationsNoUnusedKeys(t *testing.T) {
	raw := rawTranslations(t)

	defined := make(map[string]struct{}, len(raw["en"]))
	for key := range raw["en"] {
		defined[key] = struct{}{}
	}

	used := scanKeyLiterals(t, defined)

	var unused []string
	for key := range defined {
		if keyNamespaceUsed(key, used) {
			continue
		}
		if _, pending := pendingKeyAdoption[key]; pending {
			continue
		}
		unused = append(unused, key)
	}
	sort.Strings(unused)

	for _, key := range unused {
		t.Errorf("key %q is defined in all %d languages but never referenced in any .go file", key, len(localizationLangs))
	}
	if len(unused) > 0 {
		t.Errorf("%d dead keys found; a key that nothing reads should be deleted from all languages", len(unused))
	}
}

// reKeyNamespacePrefix matches the namespace part of a translation key: the
// stem up to and including the last underscore, as in "lang_set_" of
// "lang_set_it".
var reKeyNamespacePrefix = regexp.MustCompile(`^[a-z][a-z0-9_]*_$`)

// keyNamespaceUsed reports whether key is referenced directly or is built by
// concatenating a namespace that is referenced.
//
// languageSetKey returns "lang_set_" + lang, so only "lang_set_en" (the
// fallback) ever appears verbatim; the other five are unreachable for a plain
// literal comparison. Requiring the namespace shape keeps a bare "_" or "-"
// operand from masking the whole dictionary.
func keyNamespaceUsed(key string, used map[string]bool) bool {
	if used[key] {
		return true
	}
	idx := strings.LastIndex(key, "_")
	if idx < 0 {
		return false
	}
	prefix := key[:idx+1]
	if !reKeyNamespacePrefix.MatchString(prefix) {
		return false
	}
	return used[prefix]
}

// forEachGoFile parses every non-test .go file of the module and hands it to fn.
// The dictionary file and the other files named translations* are skipped.
func forEachGoFile(t *testing.T, fn func(fset *token.FileSet, file *ast.File)) {
	t.Helper()

	// The test binary runs with the working directory set to internal/app, so
	// the module root is two levels up. Getting this wrong silently scans only
	// internal/ and turns half the dictionary into false dead keys.
	root := "../.."
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(filepath.Base(path), "translations") {
			return nil
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil
		}
		fn(fset, file)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk error: %v", err)
	}
}

// callName returns the bare name of the function a call invokes: "Tr" for
// ctx.Tr(...), "Sprintf" for fmt.Sprintf(...), "" when it is not a plain
// identifier or selector.
func callName(fun ast.Expr) string {
	switch fn := fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

func isTrFunc(name string) bool {
	return name == "Tr" || name == "tr" || name == "Translate"
}

// trf is the pkg/commands helper that does fmt.Sprintf(ctx.Tr(key), args...).
// Its key is the second argument and the value always reaches fmt, so it is
// both a use and a format string.
func isTrfFunc(name string) bool {
	return name == "trf"
}

// keyArgIndex returns which argument of a translation call carries the key, or
// -1 when the call is not a translation call at all.
//
//	Tr(key)          -> 0
//	tr(key)          -> 0
//	Translate(l, k)  -> 1
//	trf(tr, key, ..) -> 1
func keyArgIndex(name string, argCount int) int {
	switch {
	case name == "Translate":
		if argCount >= 2 {
			return 1
		}
	case isTrfFunc(name):
		if argCount >= 2 {
			return 1
		}
	case isTrFunc(name):
		return 0
	default:
		return -1
	}
	return -1
}

// isFormatFunc reports whether a call turns its first argument into a format
// string. Those are the calls where a lost %s or a %.1f turns into runtime
// garbage such as %!s(float64=8).
func isFormatFunc(name string) bool {
	switch name {
	case "Sprintf", "Printf", "Errorf", "Fprintf", "Sprint", "Sprintln", "Fatalf":
		return true
	}
	return false
}

// fieldNameLiterals returns the literals that are composite-literal keys or
// struct tags. Those are config/JSON field names ("enabled",
// `json:"end_minute"`), never translation keys.
func fieldNameLiterals(file *ast.File) map[*ast.BasicLit]bool {
	out := make(map[*ast.BasicLit]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.KeyValueExpr:
			if lit, ok := v.Key.(*ast.BasicLit); ok {
				out[lit] = true
			}
		case *ast.Field:
			// ast.Field.Tag is already a *ast.BasicLit.
			if v.Tag != nil {
				out[v.Tag] = true
			}
		}
		return true
	})
	return out
}

// callSite records what the code does with one translation key.
type callSite struct {
	// used is true when the key reaches Tr/tr/Translate as a literal.
	used bool
	// isFormat is true when that Tr/tr/Translate call is an argument of
	// fmt.Sprintf and friends, i.e. the value goes through fmt and its verbs
	// must match the ones the caller passes.
	isFormat bool
}

// scanCallSites returns, for every key, whether it reaches a Tr/tr/Translate
// call and whether that call is inside a fmt.Sprintf.
//
// The distinction matters for the placeholder check. mon_cpu_crit is
// "🧠 CPU critical: `%.1f%%`": the verb sits inside a Markdown code span, and a
// scanner that strips code spans before looking for verbs loses it. top_header
// is "`PID   CPU%  MEM%  COMMAND`": the "%" there reads as the verb "% M" and
// the value is never passed to fmt. Only the call site tells the two apart, so
// format keys are checked on the raw string and the rest with code spans
// stripped.
//
// For the opposite direction — which keys are referenced at all — see
// scanKeyLiterals.
func scanCallSites(t *testing.T) map[string]callSite {
	out := make(map[string]callSite)

	record := func(key string, mutate func(*callSite)) {
		cs := out[key]
		mutate(&cs)
		out[key] = cs
	}

	forEachGoFile(t, func(_ *token.FileSet, file *ast.File) {
		isFieldName := fieldNameLiterals(file)

		// Every node that sits inside a fmt.Sprintf-family argument list.
		insideFormat := make(map[ast.Node]bool)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isFormatFunc(callName(call.Fun)) {
				return true
			}
			for _, arg := range call.Args {
				ast.Inspect(arg, func(inner ast.Node) bool {
					if inner != nil {
						insideFormat[inner] = true
					}
					return true
				})
			}
			return true
		})

		ast.Inspect(file, func(n ast.Node) bool {
			// The branch must be tested on the CallExpr node itself. Putting it
			// after an "is this node a BasicLit" early return makes it
			// unreachable: a CallExpr is never a BasicLit, so `used` stays
			// false and TestDeepTranslationAudit silently audits nothing.
			if call, ok := n.(*ast.CallExpr); ok {
				name := callName(call.Fun)
				idx := keyArgIndex(name, len(call.Args))
				if idx >= 0 && len(call.Args) > idx {
					if argLit, ok := call.Args[idx].(*ast.BasicLit); ok &&
						argLit.Kind == token.STRING && !isFieldName[argLit] {
						if key, err := strconv.Unquote(argLit.Value); err == nil {
							// trf always formats; Tr/tr only do when nested in
							// a fmt.Sprintf-family call.
							inFmt := isTrfFunc(name) ||
								insideFormat[n] || insideFormat[argLit]
							record(key, func(cs *callSite) {
								cs.used = true
								cs.isFormat = cs.isFormat || inFmt
							})
						}
					}
				}
			}

			// A key can reach a Tr call through a variable
			// (languageSetKey returns "lang_set_" + lang), so the dead-key
			// direction needs every literal, not just the call-site ones: see
			// scanKeyLiterals.
			return true
		})
	})

	return out
}

// scanKeyLiterals returns the set of literals that appear anywhere in the
// module, plus the namespace prefixes used to build keys by concatenation
// ("lang_set_" + lang). This is what the dead-key check needs: a key can be
// reached without a Tr call at all.
func scanKeyLiterals(t *testing.T, candidates map[string]struct{}) map[string]bool {
	t.Helper()

	used := make(map[string]bool, len(candidates))
	forEachGoFile(t, func(_ *token.FileSet, file *ast.File) {
		isFieldName := fieldNameLiterals(file)
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || isFieldName[lit] {
				return true
			}
			key, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if _, wanted := candidates[key]; wanted {
				used[key] = true
				return true
			}
			if reKeyNamespacePrefix.MatchString(key) {
				used[key] = true
			}
			return true
		})
	})
	return used
}
