package i18n_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The catalogs are complete for the code: every message the Go code asks the
// i18n package for is in zapfast.pot, has a translation in every language
// (the generator drops fuzzy and empty ones, which would show English), and
// keeps the msgid's {placeholders}.
//
// The code's messages are the literals (and string constants, and
// concatenations of them) passed to i18n.T, P, N, TIn, PIn and NIn, to the
// local names bound to them (T := i18n.T) and to the one-line wrappers
// (func tr(s string) string { return i18n.T(s) }).
//
// A call whose message comes from a table (the settings rows, the shortcut
// list, the label colours, the picker's names) is checked through tables:
// the source strings of each are collected from the code by the specs
// below. A dynamic call in a file no spec covers fails the test, so a new
// table cannot go unnoticed. Two calls are not checkable at all: the core's
// own error text (state/events.go, translated if upstream has the string)
// and the number-lookup failure (ui/dialogs/newchat.go, a value of the
// dialog's state).

const poDir = "../../assets/i18n"

var languages = []string{"de", "es", "fr", "id", "it", "pt-BR", "ru", "tr", "zh-Hans", "zh-Hant"}

type key struct{ ctx, id string }

type poEntry struct {
	plural  string
	strs    []string
	fuzzy   bool
	hasPlur bool
}

func TestEveryMessageOfTheCodeIsTranslated(t *testing.T) {
	used, dynamic := codeMessages(t)
	nTable := 0
	for k, where := range tableMessages(t) {
		if _, ok := used[k]; !ok {
			used[k] = where
			nTable++
		}
	}
	covered := map[string]bool{
		"state/events.go":       true, // the core's error text
		"ui/dialogs/newchat.go": true, // the number-lookup failure
	}
	for _, sp := range tableSpecs {
		covered[sp.file] = true
	}
	// the files that read a table the specs find elsewhere
	covered["ui/settings/texts.go"] = true // secTitle
	covered["ui/dialogs/about.go"] = true  // screen.Shortcuts
	for _, d := range dynamic {
		if !covered[d[:strings.LastIndex(d, ":")]] {
			t.Errorf("%s: a message built at run time, in a file no table spec covers: add a spec", d)
		}
	}
	if len(used) < 500 {
		t.Fatalf("found only %d messages in the code: the scan is broken", len(used))
	}
	t.Logf("%d messages in the code and its tables (%d from the tables)", len(used), nTable)

	pot := readPO(t, filepath.Join(poDir, "zapfast.pot"))
	cats := map[string]map[key]poEntry{}
	for _, l := range languages {
		cats[l] = readPO(t, filepath.Join(poDir, l+".po"))
	}
	keys := make([]key, 0, len(used))
	for k := range used {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ctx != keys[j].ctx {
			return keys[i].ctx < keys[j].ctx
		}
		return keys[i].id < keys[j].id
	})
	for _, k := range keys {
		where := used[k]
		src, ok := pot[k]
		if !ok {
			t.Errorf("%s: %q is not in zapfast.pot", where, label(k))
			continue
		}
		want := placeholders(k.id)
		for _, l := range languages {
			e, ok := cats[l][k]
			switch {
			case !ok:
				t.Errorf("%s: %q has no entry in %s.po", where, label(k), l)
				continue
			case e.fuzzy:
				t.Errorf("%s: %q is fuzzy in %s.po", where, label(k), l)
				continue
			}
			if src.hasPlur && len(e.strs) < 1 {
				t.Errorf("%s: %q has no plural forms in %s.po", where, label(k), l)
			}
			for i, s := range e.strs {
				if s == "" {
					t.Errorf("%s: %q has an empty msgstr[%d] in %s.po", where, label(k), i, l)
				} else if got := placeholders(s); got != want {
					t.Errorf("%s: %q in %s.po has placeholders %s, the msgid has %s: %q", where, label(k), l, got, want, s)
				}
			}
		}
	}
}

func label(k key) string {
	if k.ctx == "" {
		return k.id
	}
	return k.ctx + "|" + k.id
}

var placeholderRE = regexp.MustCompile(`\{[A-Za-z0-9_]*\}`)

// placeholders is the sorted list of the {placeholders} in s.
func placeholders(s string) string {
	p := placeholderRE.FindAllString(s, -1)
	sort.Strings(p)
	return strings.Join(p, " ")
}

// readPO reads a .po/.pot file's messages (msgctxt, msgid, msgid_plural,
// msgstr and msgstr[n], with continuation lines and the fuzzy flag).
func readPO(t *testing.T, path string) map[key]poEntry {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[key]poEntry{}
	var (
		ctx, id string
		e       poEntry
		field   *string
		fuzzy   bool
		open    bool
	)
	flush := func() {
		if open && id != "" {
			e.fuzzy = fuzzy
			out[key{ctx, id}] = e
		}
		ctx, id, e, field, fuzzy, open = "", "", poEntry{}, nil, false, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "#,"):
			if strings.Contains(line, "fuzzy") {
				fuzzy = true
			}
		case strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "msgctxt "):
			open, field = true, &ctx
			*field = unq(t, line[len("msgctxt "):])
		case strings.HasPrefix(line, "msgid_plural "):
			e.hasPlur = true
			field = &e.plural
			*field = unq(t, line[len("msgid_plural "):])
		case strings.HasPrefix(line, "msgid "):
			open, field = true, &id
			*field = unq(t, line[len("msgid "):])
		case strings.HasPrefix(line, "msgstr["):
			i := strings.Index(line, "] ")
			e.strs = append(e.strs, unq(t, line[i+2:]))
			field = &e.strs[len(e.strs)-1]
		case strings.HasPrefix(line, "msgstr "):
			e.strs = append(e.strs[:0], unq(t, line[len("msgstr "):]))
			field = &e.strs[0]
		case strings.HasPrefix(line, `"`):
			if field != nil {
				*field += unq(t, line)
			}
		}
	}
	flush()
	// The header (msgid "") is not a message.
	return out
}

func unq(t *testing.T, s string) string {
	t.Helper()
	u, err := strconv.Unquote(strings.TrimSpace(s))
	if err != nil {
		t.Fatalf("bad PO string %s: %v", s, err)
	}
	return u
}

// arguments of the entry points: where the context, the msgid and the plural
// are among the call's arguments (-1 for none).
var entry = map[string][3]int{
	"T": {-1, 0, -1}, "P": {0, 1, -1}, "N": {-1, 0, 1},
	"TIn": {-1, 1, -1}, "PIn": {1, 2, -1}, "NIn": {-1, 1, 2},
}

// codeMessages is the messages the code asks for, each with where, and the
// places that ask for one built at run time.
func codeMessages(t *testing.T) (map[key]string, []string) {
	t.Helper()
	root := ".."
	dirs := map[string][]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != root {
			if n := d.Name(); n == "tools" || n == "testdata" || n == "i18n" || strings.HasPrefix(n, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			dirs[filepath.Dir(p)] = append(dirs[filepath.Dir(p)], p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	used := map[key]string{}
	var dynamic []string
	fset := token.NewFileSet()
	for dir, paths := range dirs {
		var files []*ast.File
		for _, p := range paths {
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		consts := stringConsts(files)
		wrappers := map[string]string{} // package-level wrapper func -> entry point
		for _, f := range files {
			if importsI18n(f) {
				for _, d := range f.Decls {
					if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
						if name := wrapperOf(fd); name != "" {
							wrappers[fd.Name.Name] = name
						}
					}
				}
			}
		}
		for _, f := range files {
			if !importsI18n(f) {
				continue
			}
			bodies := map[*ast.CallExpr]bool{} // the wrappers' own calls
			for _, d := range f.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && wrapperOf(fd) != "" {
					bodies[fd.Body.List[0].(*ast.ReturnStmt).Results[0].(*ast.CallExpr)] = true
				}
			}
			alias := map[string]string{} // local name -> entry point
			ast.Inspect(f, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok || len(as.Lhs) != len(as.Rhs) {
					return true
				}
				for i, r := range as.Rhs {
					if name := i18nName(r); name != "" {
						if id, ok := as.Lhs[i].(*ast.Ident); ok {
							alias[id.Name] = name
						}
					}
				}
				return true
			})
			ast.Inspect(f, func(n ast.Node) bool {
				ce, ok := n.(*ast.CallExpr)
				if !ok || bodies[ce] {
					return true
				}
				name := i18nName(ce.Fun)
				if id, ok := ce.Fun.(*ast.Ident); ok && name == "" {
					if name = alias[id.Name]; name == "" {
						name = wrappers[id.Name]
					}
				}
				idx, ok := entry[name]
				if !ok {
					return true
				}
				pos := fset.Position(ce.Pos())
				where := strings.TrimPrefix(filepath.ToSlash(pos.Filename), "../") + ":" + strconv.Itoa(pos.Line)
				var parts [3]string
				for i, a := range idx {
					if a < 0 {
						continue
					}
					if a >= len(ce.Args) {
						dynamic = append(dynamic, where)
						return true
					}
					s, ok := constString(ce.Args[a], consts)
					if !ok {
						dynamic = append(dynamic, where)
						return true
					}
					parts[i] = s
				}
				used[key{parts[0], parts[1]}] = where
				_ = dir
				return true
			})
		}
	}
	return used, dynamic
}

// A tableSpec says where the source strings of a table are in the code.
type tableSpec struct {
	file  string // relative to gnui
	kind  string
	name  string // a variable, function or call
	ident string // for "assign": the variable the strings are assigned to
	arg   int    // for "call" and "field": the argument or field
	ctx   string // the messages' context
}

const (
	specVar    = "var"    // the string literals of package variable name
	specAssign = "assign" // the literals assigned to ident in function name
	specCall   = "call"   // the literal arguments arg of calls of name
	specField  = "field"  // field arg of the literals of struct type name
	specTx     = "tx"     // ui/settings' tx{ctx, id} values and m(id) calls, in the package of file
)

var tableSpecs = []tableSpec{
	{file: "ui/settings/rows.go", kind: specTx},
	{file: "ui/settings/rows.go", kind: specVar, name: "secTitle"},
	{file: "ui/composer/attach.go", kind: specVar, name: "toolNames"},
	{file: "ui/composer/strips.go", kind: specAssign, name: "summary", ident: "what"},
	{file: "ui/picker/picker.go", kind: specVar, name: "tabNames"},
	{file: "ui/picker/gifs.go", kind: specAssign, name: "giphyKeyPrompt", ident: "text"},
	{file: "ui/picker/stickers.go", kind: specCall, name: "shelfTab", arg: 1},
	{file: "ui/screen/keys.go", kind: specField, name: "Shortcut", arg: 1},
	{file: "ui/dialogs/labels.go", kind: specAssign, name: "colorName", ident: "names", ctx: "label colour"},
	{file: "ui/conversation/menu.go", kind: specAssign, name: "", ident: "open"},
}

// tableMessages are the messages the specs find, each with where.
func tableMessages(t *testing.T) map[key]string {
	t.Helper()
	out := map[key]string{}
	fset := token.NewFileSet()
	for _, sp := range tableSpecs {
		path := filepath.Join("..", filepath.FromSlash(sp.file))
		var paths []string
		if sp.kind == specTx {
			paths, _ = filepath.Glob(filepath.Join(filepath.Dir(path), "*.go"))
		} else {
			paths = []string{path}
		}
		found := 0
		add := func(pos token.Pos, ctx, id string) {
			if id == "" {
				return
			}
			p := fset.Position(pos)
			out[key{ctx, id}] = strings.TrimPrefix(filepath.ToSlash(p.Filename), "../") + ":" + strconv.Itoa(p.Line)
			found++
		}
		lit := func(e ast.Expr) (string, bool) {
			if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
				s, err := strconv.Unquote(b.Value)
				return s, err == nil
			}
			return "", false
		}
		allLits := func(n ast.Node, ctx string) {
			ast.Inspect(n, func(n ast.Node) bool {
				if b, ok := n.(*ast.BasicLit); ok {
					if s, ok := lit(b); ok {
						add(b.Pos(), ctx, s)
					}
				}
				return true
			})
		}
		for _, p := range paths {
			if strings.HasSuffix(p, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			switch sp.kind {
			case specVar:
				for _, d := range f.Decls {
					if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.VAR {
						for _, vs := range gd.Specs {
							for i, n := range vs.(*ast.ValueSpec).Names {
								if n.Name == sp.name && i < len(vs.(*ast.ValueSpec).Values) {
									allLits(vs.(*ast.ValueSpec).Values[i], sp.ctx)
								}
							}
						}
					}
				}
			case specAssign:
				for _, d := range f.Decls {
					fd, ok := d.(*ast.FuncDecl)
					if !ok || fd.Body == nil || (sp.name != "" && fd.Name.Name != sp.name) {
						continue
					}
					ast.Inspect(fd.Body, func(n ast.Node) bool {
						if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == len(as.Rhs) {
							for i, l := range as.Lhs {
								if id, ok := l.(*ast.Ident); ok && id.Name == sp.ident {
									allLits(as.Rhs[i], sp.ctx)
								}
							}
						}
						return true
					})
				}
			case specCall:
				ast.Inspect(f, func(n ast.Node) bool {
					if ce, ok := n.(*ast.CallExpr); ok && sp.arg < len(ce.Args) {
						name := ""
						switch fn := ce.Fun.(type) {
						case *ast.SelectorExpr:
							name = fn.Sel.Name
						case *ast.Ident:
							name = fn.Name
						}
						if name == sp.name {
							if s, ok := lit(ce.Args[sp.arg]); ok {
								add(ce.Args[sp.arg].Pos(), sp.ctx, s)
							}
						}
					}
					return true
				})
			case specField:
				ast.Inspect(f, func(n ast.Node) bool {
					cl, ok := n.(*ast.CompositeLit)
					if !ok {
						return true
					}
					typed := false
					if id, ok := cl.Type.(*ast.Ident); ok && id.Name == sp.name {
						typed = true
					}
					// the elements of [...]Shortcut{{…}, …} have no type of their own
					if at, ok := cl.Type.(*ast.ArrayType); ok {
						if id, ok := at.Elt.(*ast.Ident); ok && id.Name == sp.name {
							for _, el := range cl.Elts {
								if c, ok := el.(*ast.CompositeLit); ok && sp.arg < len(c.Elts) {
									if s, ok := lit(c.Elts[sp.arg]); ok {
										add(c.Elts[sp.arg].Pos(), sp.ctx, s)
									}
								}
							}
						}
					}
					if typed && sp.arg < len(cl.Elts) {
						if s, ok := lit(cl.Elts[sp.arg]); ok {
							add(cl.Elts[sp.arg].Pos(), sp.ctx, s)
						}
					}
					return true
				})
			case specTx:
				isTx := func(e ast.Expr) bool {
					id, ok := e.(*ast.Ident)
					return ok && id.Name == "tx"
				}
				fromLit := func(cl *ast.CompositeLit) {
					var vals []string
					ctx, id := "", ""
					for i, el := range cl.Elts {
						if kv, ok := el.(*ast.KeyValueExpr); ok {
							s, _ := lit(kv.Value)
							switch kv.Key.(*ast.Ident).Name {
							case "ctx":
								ctx = s
							case "id":
								id = s
							}
						} else if s, ok := lit(el); ok {
							vals = append(vals, s)
						} else if ix, ok := el.(*ast.IndexExpr); ok && i == 1 && len(vals) == 1 {
							// tx{"privacy", [7]string{…}[i]}
							if arr, ok := ix.X.(*ast.CompositeLit); ok {
								for _, e := range arr.Elts {
									if s, ok := lit(e); ok {
										add(e.Pos(), vals[0], s)
									}
								}
							}
							return
						} else if i < 2 {
							return
						}
					}
					if len(vals) == 2 {
						ctx, id = vals[0], vals[1]
					} else if len(vals) == 1 {
						id = vals[0]
					}
					add(cl.Pos(), ctx, id)
				}
				ast.Inspect(f, func(n ast.Node) bool {
					switch x := n.(type) {
					case *ast.CompositeLit:
						if isTx(x.Type) {
							fromLit(x)
						}
						if at, ok := x.Type.(*ast.ArrayType); ok && isTx(at.Elt) {
							for _, el := range x.Elts {
								if c, ok := el.(*ast.CompositeLit); ok && c.Type == nil {
									fromLit(c)
								}
							}
						}
					case *ast.CallExpr:
						if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "m" && len(x.Args) == 1 {
							if s, ok := lit(x.Args[0]); ok {
								add(x.Pos(), "", s)
							}
						}
					}
					return true
				})
			}
		}
		if found == 0 {
			t.Errorf("table spec %+v found no message: the table moved?", sp)
		}
	}
	// ui/settings' privacy titles are an indexed array literal in privTitle.
	return out
}

func importsI18n(f *ast.File) bool {
	for _, im := range f.Imports {
		if strings.HasSuffix(strings.Trim(im.Path.Value, `"`), "/i18n") {
			return true
		}
	}
	return false
}

// i18nName is "T", "P" … when e is i18n.T, i18n.P …
func i18nName(e ast.Expr) string {
	if sel, ok := e.(*ast.SelectorExpr); ok {
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == "i18n" {
			if _, ok := entry[sel.Sel.Name]; ok {
				return sel.Sel.Name
			}
		}
	}
	return ""
}

// wrapperOf is the entry point f wraps if it is func f(s string) string
// { return i18n.T(s) } (or P with a context first).
func wrapperOf(fd *ast.FuncDecl) string {
	if fd.Body == nil || len(fd.Body.List) != 1 {
		return ""
	}
	r, ok := fd.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(r.Results) != 1 {
		return ""
	}
	ce, ok := r.Results[0].(*ast.CallExpr)
	if !ok {
		return ""
	}
	if name := i18nName(ce.Fun); name == "T" && len(ce.Args) == 1 && len(fd.Type.Params.List) == 1 && len(fd.Type.Params.List[0].Names) == 1 {
		if id, ok := ce.Args[0].(*ast.Ident); ok && id.Name == fd.Type.Params.List[0].Names[0].Name {
			return name
		}
	}
	return ""
}

// stringConsts are the package's string constants by name (constants of
// different functions with one name are told apart by nothing: the code
// avoids that).
func stringConsts(files []*ast.File) map[string]string {
	out := map[string]string{}
	for pass := 0; pass < 3; pass++ {
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				gd, ok := n.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					return true
				}
				for _, sp := range gd.Specs {
					vs := sp.(*ast.ValueSpec)
					for i, nm := range vs.Names {
						if i < len(vs.Values) {
							if s, ok := constString(vs.Values[i], out); ok {
								out[nm.Name] = s
							}
						}
					}
				}
				return true
			})
		}
	}
	return out
}

// constString is e's value if it is a string literal, a constant of the
// package, or a concatenation of those.
func constString(e ast.Expr, consts map[string]string) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			s, err := strconv.Unquote(x.Value)
			return s, err == nil
		}
	case *ast.Ident:
		s, ok := consts[x.Name]
		return s, ok
	case *ast.ParenExpr:
		return constString(x.X, consts)
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			a, ok1 := constString(x.X, consts)
			b, ok2 := constString(x.Y, consts)
			return a + b, ok1 && ok2
		}
	}
	return "", false
}
