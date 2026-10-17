//go:build loop

// Command loopfn prints per-function complexity, so the loop can aim at the worst
// function rather than the worst file.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
)

func compound(n ast.Node) bool {
	switch n.(type) {
	case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt,
		*ast.TypeSwitchStmt, *ast.SelectStmt, *ast.CaseClause, *ast.CommClause:
		return true
	}
	return false
}

func decisions(body ast.Node) int {
	n := 0
	ast.Inspect(body, func(node ast.Node) bool {
		switch s := node.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt,
			*ast.TypeSwitchStmt, *ast.SelectStmt, *ast.CaseClause, *ast.CommClause:
			n++
		case *ast.BinaryExpr:
			if s.Op == token.LAND || s.Op == token.LOR {
				n++
			}
		}
		return true
	})
	return n
}

type row struct {
	name string
	n    int
}

func main() {
	fset := token.NewFileSet()
	totals := map[string]int{}
	for _, path := range os.Args[1:] {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			if fn.Recv != nil {
				name = "(m)" + name
			}
			totals[path+":"+name] = 1 + decisions(fn.Body)
		}
	}
	var rows []row
	for k, v := range totals {
		rows = append(rows, row{k, v})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].n > rows[j].n })
	for _, r := range rows {
		fmt.Printf("%3d  %s\n", r.n, r.name)
	}
}
