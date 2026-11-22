package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This file is the configuration census (DF-RABBIT-HOLE-011). It exists
// because RABBITHOLE_* keys kept being parsed and validated without ever being
// read by the running system: RABBITHOLE_CORS_ORIGINS is the dangerous example,
// where an operator restricting origins still got `Access-Control-Allow-Origin:
// *`. A key that no code outside internal/config reads is a lie told to the
// operator, so the census fails the build instead.
//
// The census is static (go/parser over the module source — no shelling out, no
// network, no reflection on runtime state) and checks three invariants:
//
//  1. every Config field names exactly one RABBITHOLE_* key in its comment;
//  2. that key is parsed by Load() via os.Getenv;
//  3. that field is read by non-test code outside internal/config, so setting
//     the variable changes what the daemon does.
//
// Invariant 3 is what catches a newly added dead key. Fields consumed inside
// Load() itself (DataDir feeds the DBPath/ModelPath derivation) are exempted
// explicitly in derivedConsumers, with the reason and the test that proves the
// derivation.

// derivedConsumers lists Config fields that have no reader outside
// internal/config because their effect is materialised into other fields by
// Load() itself. Keep this list empty unless the derivation is real and tested.
var derivedConsumers = map[string]string{
	"DataDir": "Load() derives DBPath and ModelPath from DataDir (config.go); " +
		"the derived values are what cmd reads. Proven by TestLoadDataDirDerivesPaths.",
}

// envKeyRe matches an environment variable name in a field comment.
var envKeyRe = regexp.MustCompile(`RABBITHOLE_[A-Z0-9_]+`)

// configField is one field of config.Config with the env key its comment names.
type configField struct {
	Name   string
	EnvKey string
	Line   int
}

// TestConfigFieldCensus fails when a Config field or an env key is not wired
// end to end.
func TestConfigFieldCensus(t *testing.T) {
	cfgFile := "config.go"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, cfgFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", cfgFile, err)
	}

	fields := configFields(t, fset, file)
	parsed := parsedEnvKeys(t, file)

	// Cross-check the source scan against the compiled struct: if the parser
	// and reflection disagree, every later assertion would be running against
	// the wrong list.
	assertFieldsMatchReflection(t, fields)

	// Invariant 1: one documented key per field.
	for _, f := range fields {
		if f.EnvKey == "" {
			t.Errorf("config.Config.%s (config.go:%d) has no RABBITHOLE_* key in its comment", f.Name, f.Line)
		}
	}

	// Invariant 2: every documented key is parsed by Load(), and vice versa.
	byKey := make(map[string]configField, len(fields))
	for _, f := range fields {
		if f.EnvKey != "" {
			byKey[f.EnvKey] = f
			if !parsed[f.EnvKey] {
				t.Errorf("config.Config.%s documents %s but Load() never calls os.Getenv(%q)", f.Name, f.EnvKey, f.EnvKey)
			}
		}
	}
	for key := range parsed {
		if _, ok := byKey[key]; !ok {
			t.Errorf("Load() parses %s but no config.Config field documents it (leftover key after removal?)", key)
		}
	}

	// Invariant 3: every field is read outside internal/config, so the value
	// actually reaches the running system.
	consumed := moduleConfigReaders(t)
	var dead []string
	for _, f := range fields {
		if _, exempt := derivedConsumers[f.Name]; exempt {
			continue
		}
		if consumed[f.Name] {
			continue
		}
		dead = append(dead, f.Name+" ("+f.EnvKey+")")
	}
	if len(dead) > 0 {
		sort.Strings(dead)
		t.Fatalf("config fields parsed but never read outside internal/config — wire them into the system or delete field+env parse+doc line:\n  %s",
			strings.Join(dead, "\n  "))
	}
}

// configFields extracts the Config struct's fields and the RABBITHOLE_* key
// each field comment names.
func configFields(t *testing.T, fset *token.FileSet, file *ast.File) []configField {
	t.Helper()

	var out []configField
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "Config" {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("Config is not a struct")
			}
			for _, field := range st.Fields.List {
				name := ""
				if len(field.Names) > 0 {
					name = field.Names[0].Name
				} else {
					t.Fatalf("Config contains an embedded field; the census needs named fields")
				}

				commentParts := []string{}
				if field.Doc != nil {
					commentParts = append(commentParts, field.Doc.Text())
				}
				if field.Comment != nil {
					commentParts = append(commentParts, field.Comment.Text())
				}
				keys := envKeyRe.FindAllString(strings.Join(commentParts, " "), -1)

				cf := configField{Name: name, Line: fset.Position(field.Pos()).Line}
				if len(keys) == 1 {
					cf.EnvKey = keys[0]
				} else if len(keys) > 1 {
					t.Errorf("config.Config.%s documents %d env keys (%v); each field must name exactly one",
						name, len(keys), keys)
				}
				out = append(out, cf)
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("no Config struct found in config.go")
	}
	return out
}

// assertFieldsMatchReflection verifies the parser found exactly the fields the
// compiled Config type has, in declaration order.
func assertFieldsMatchReflection(t *testing.T, fields []configField) {
	t.Helper()

	typ := reflect.TypeOf(Config{})
	if typ.NumField() != len(fields) {
		t.Fatalf("source scan found %d Config fields, reflection sees %d", len(fields), typ.NumField())
	}
	for i := 0; i < typ.NumField(); i++ {
		if got, want := fields[i].Name, typ.Field(i).Name; got != want {
			t.Fatalf("field %d: source scan says %q, reflection says %q", i, got, want)
		}
	}
}

// parsedEnvKeys returns every literal passed to os.Getenv inside Load().
func parsedEnvKeys(t *testing.T, file *ast.File) map[string]bool {
	t.Helper()

	keys := make(map[string]bool)
	var load *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "Load" && fn.Recv == nil {
			load = fn
			break
		}
	}
	if load == nil || load.Body == nil {
		t.Fatalf("Load() not found in config.go")
	}

	ast.Inspect(load.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Getenv" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "os" || len(call.Args) != 1 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Errorf("os.Getenv called with a non-literal argument")
			return true
		}
		keys[strings.Trim(lit.Value, `"`)] = true
		return true
	})
	return keys
}

// moduleConfigReaders walks the module's non-test source outside internal/config
// and returns the set of Config field names that some `cfg.<Field>` selector
// reads.
//
// Receivers are resolved structurally: an identifier is treated as a
// config.Config value when it comes from config.Load(), config.Defaults(),
// the CLI's loadConfig(), or an explicit config.Config declaration/parameter.
// That keeps the scan deterministic and dependency-free (no go/types load of
// third-party packages, no shelling out) while covering every consumer shape in
// this module: the only importer of internal/config is cmd/rabbit-hole.
//
// The scan is deliberately conservative in one direction: it can report a field
// as read when a same-named receiver shadows the config value. It can never
// report a live field as unread, which is the failure this census guards
// against.
func moduleConfigReaders(t *testing.T) map[string]bool {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}

	read := make(map[string]bool)
	fset := token.NewFileSet()

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "web", "deploy", "specs", "testdata", ".coding-hermes":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/config/") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		receivers := configReceivers(file)
		if len(receivers) == 0 {
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && receivers[id.Name] {
				read[sel.Sel.Name] = true
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk module source: %v", err)
	}
	if len(read) == 0 {
		t.Fatalf("census found no config readers under %s — the scan is broken, not the code", root)
	}
	return read
}

// configReceivers returns the identifiers in one file that hold a
// config.Config value.
func configReceivers(file *ast.File) map[string]bool {
	receivers := make(map[string]bool)

	// isConfigType reports whether an expression names config.Config (or a
	// pointer to it).
	isConfigType := func(expr ast.Expr) bool {
		if star, ok := expr.(*ast.StarExpr); ok {
			expr = star.X
		}
		sel, ok := expr.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Config" {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "config"
	}

	// isConfigCall reports whether an expression calls something that returns
	// a config.Config: config.Load(), config.Defaults(), loadConfig().
	isConfigCall := func(expr ast.Expr) bool {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			pkg, ok := fn.X.(*ast.Ident)
			return ok && pkg.Name == "config" && (fn.Sel.Name == "Load" || fn.Sel.Name == "Defaults")
		case *ast.Ident:
			return fn.Name == "loadConfig"
		}
		return false
	}

	// addTargets records the left-hand identifiers of an assignment/var decl.
	addTargets := func(lhs []ast.Expr, rhs []ast.Expr, valueIsConfig bool) {
		for i, target := range lhs {
			id, ok := target.(*ast.Ident)
			if !ok || id.Name == "_" {
				continue
			}
			config := valueIsConfig
			if i < len(rhs) && isConfigCall(rhs[i]) {
				config = true
			}
			if config {
				receivers[id.Name] = true
			}
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			addTargets(node.Lhs, node.Rhs, false)
		case *ast.ValueSpec:
			addTargets(
				identsToExprs(node.Names),
				node.Values,
				node.Type != nil && isConfigType(node.Type),
			)
		case *ast.FuncDecl:
			for _, param := range node.Type.Params.List {
				if !isConfigType(param.Type) {
					continue
				}
				for _, name := range param.Names {
					receivers[name.Name] = true
				}
			}
		}
		return true
	})

	return receivers
}

func identsToExprs(ids []*ast.Ident) []ast.Expr {
	out := make([]ast.Expr, 0, len(ids))
	for _, id := range ids {
		out = append(out, id)
	}
	return out
}
