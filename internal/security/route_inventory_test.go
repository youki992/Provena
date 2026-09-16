package security

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
)

// TestEveryProtectedRouteHasCatalogPermission audits the web console's route
// table against the permission catalog.
//
// The table lives in internal/app/routes.go (compiled only with the webconsole
// tag) while the rest of the wiring stays in app.go, so the whole package is
// parsed instead of one file: moving a registration between files must not be
// able to silently drop it out of the audit.
func TestEveryProtectedRouteHasCatalogPermission(t *testing.T) {
	packages, err := parser.ParseDir(token.NewFileSet(), filepath.Join("..", "app"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string]string{
		"GET": http.MethodGet, "POST": http.MethodPost, "PUT": http.MethodPut,
		"PATCH": http.MethodPatch, "DELETE": http.MethodDelete,
	}
	prefixes := map[string]string{"protected": "", "c2Routes": "/c2", "knowledgeRoutes": "/knowledge"}
	found := 0
	files := 0
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			files++
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				prefix, protected := prefixes[ident.Name]
				method, routeMethod := methods[sel.Sel.Name]
				literal, literalPath := call.Args[0].(*ast.BasicLit)
				if !protected || !routeMethod || !literalPath || literal.Kind != token.STRING {
					return true
				}
				path, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Errorf("invalid route literal %s", literal.Value)
					return true
				}
				found++
				permission := permissionForRequest(method, "/api"+prefix+path)
				if permission == "" {
					t.Errorf("unmapped protected route: %s %s%s", method, prefix, path)
				} else if _, ok := PermissionCatalog[permission]; !ok {
					t.Errorf("route %s %s%s maps to unknown permission %q", method, prefix, path, permission)
				}
				return true
			})
		}
	}
	if files == 0 {
		t.Fatal("no files were parsed under internal/app; the route audit is not looking at anything")
	}
	if found < 100 {
		t.Fatalf("route inventory unexpectedly small: %d", found)
	}
}
