package task

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestHiddenValidateEmail(t *testing.T) {
	for _, ok := range []string{"a@b", "ada@example.com"} {
		if validateEmail(ok) != nil || UpdateProfile(ok) != nil {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "@b", "a@", "a@@b", "a b@c", "noat"} {
		if validateEmail(bad) != ErrInvalidEmail || UpdateProfile(bad) != ErrInvalidEmail || SignUp("x", bad) != ErrInvalidEmail {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestHiddenNoDuplicateValidation(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "users.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name == "validateEmail" {
			continue
		}
		var calls bool
		ast.Inspect(fn, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "validateEmail" {
					calls = true
				}
				if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "strings" && (sel.Sel.Name == "Index" || sel.Sel.Name == "Count") {
						t.Errorf("%s still validates the email inline", fn.Name.Name)
					}
				}
			}
			return true
		})
		if !calls {
			t.Errorf("%s does not call validateEmail", fn.Name.Name)
		}
	}
}
