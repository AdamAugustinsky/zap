package ui_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	ztheme "zapfast/theme"
)

// TestSpacingGrid holds the screens to Zap's spacing grid (theme/space.go):
// every literal a gap, a padding, an inset or a margin is built from is a
// step of the grid. It reads the literals of
//   - the calls that space a layout (NextGap, NextPadding, Space and their
//     kin), directly or through Pt;
//   - the fields of option structs that do (GapX, PadY, Inset…);
//   - the constants and variables named for a gap, a padding, an inset, a
//     margin or a gutter.
//
// A literal that is a factor (x*2, w/3) is not a space and is skipped. A
// line that must stay off the grid (a size measured from AppKit, a glyph's
// optical centre) says so with a "grid:ok" comment and its reason.
func TestSpacingGrid(t *testing.T) {
	fset := token.NewFileSet()
	spaceCalls := map[string]bool{"NextGap": true, "NextPadding": true, "Space": true, "NextSpace": true, "Gap": true, "Padding": true}
	spaceFields := map[string]bool{"Gap": true, "GapX": true, "GapY": true, "Pad": true, "PadX": true, "PadY": true, "Inset": true, "InsetX": true, "InsetY": true, "Margin": true, "Gutter": true}
	spaceName := regexp.MustCompile(`(?i)(gap|pad|padding|inset|margin|gutter)(pt)?$`)
	var bad []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		waived := map[int]bool{}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if strings.Contains(c.Text, "grid:ok") {
					waived[fset.Position(c.Pos()).Line] = true
				}
			}
		}
		check := func(e ast.Expr, what string) {
			eachLit(e, func(l *ast.BasicLit) {
				v, err := strconv.ParseFloat(l.Value, 32)
				if err != nil || ztheme.OnGrid(float32(v)) {
					return
				}
				p := fset.Position(l.Pos())
				if !waived[p.Line] {
					bad = append(bad, p.String()+": "+l.Value+" in "+what)
				}
			})
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if s, ok := n.Fun.(*ast.SelectorExpr); ok && spaceCalls[s.Sel.Name] {
					for _, a := range n.Args {
						check(a, s.Sel.Name)
					}
				}
			case *ast.KeyValueExpr:
				if id, ok := n.Key.(*ast.Ident); ok && spaceFields[id.Name] {
					check(n.Value, id.Name)
				}
			case *ast.ValueSpec:
				for i, name := range n.Names {
					if i < len(n.Values) && spaceName.MatchString(name.Name) {
						check(n.Values[i], name.Name)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("off the spacing grid: %s", b)
	}
	if len(bad) > 0 {
		t.Logf("%d literals off the grid (steps: 0, 2, and multiples of 4; theme/space.go)", len(bad))
	}
}

// eachLit calls fn for each number literal e is built from, but not for the
// factors of a product or a quotient.
func eachLit(e ast.Expr, fn func(*ast.BasicLit)) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind == token.INT || e.Kind == token.FLOAT {
			fn(e)
		}
	case *ast.ParenExpr:
		eachLit(e.X, fn)
	case *ast.UnaryExpr:
		eachLit(e.X, fn)
	case *ast.BinaryExpr:
		if e.Op == token.MUL || e.Op == token.QUO {
			if _, ok := e.X.(*ast.BasicLit); !ok {
				eachLit(e.X, fn)
			}
			if _, ok := e.Y.(*ast.BasicLit); !ok {
				eachLit(e.Y, fn)
			}
			return
		}
		eachLit(e.X, fn)
		eachLit(e.Y, fn)
	case *ast.CallExpr:
		// e.Pt(8), k.Pt(8), float32(8), max(a, 8)
		for _, a := range e.Args {
			eachLit(a, fn)
		}
	}
}
