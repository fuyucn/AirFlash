//go:build loop

// Command loopmetric is the optimize-loop's metric tape measure for this
// repository's Go code: cyclomatic complexity per function, summed per file,
// from the compiler's own go/ast parse.
//
// The shipped skill tool measures Python; this is the Go binding the skill's
// escape hatch allows. It is behind the `loop` build tag so it is not part of
// the shipped binary or the release build.
//
//	go run -tags loop ./cmd/loopmetric main.go ui/shell.go
//
// Prints one JSON object:
//
//	{"complexity":N,"max_nesting":N,"loc":N,"functions":N,"per_file":{...}}
//
// complexity is the signal the loop minimizes; max_nesting and loc are the
// lexicographic tie-breakers.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: loopmetric <file.go> ...")
		os.Exit(2)
	}
	rep := report{PerFile: map[string]*fileMetric{}}
	failed := false
	for _, path := range os.Args[1:] {
		fm := fileOf(path)
		rep.PerFile[path] = fm
		if fm.Err != "" {
			// A parse failure is a failed iteration, not a silent zero.
			failed = true
			continue
		}
		rep.Complexity += fm.Complexity
		if fm.MaxNesting > rep.MaxNesting {
			rep.MaxNesting = fm.MaxNesting
		}
		rep.Loc += fm.Loc
		rep.Functions += fm.Functions
	}
	out, _ := json.Marshal(rep)
	fmt.Println(string(out))
	if failed {
		os.Exit(1)
	}
}

type fileMetric struct {
	Complexity int    `json:"complexity"`
	MaxNesting int    `json:"max_nesting"`
	Loc        int    `json:"loc"`
	Functions  int    `json:"functions"`
	Err        string `json:"error,omitempty"`
}

type report struct {
	Complexity int                    `json:"complexity"`
	MaxNesting int                    `json:"max_nesting"`
	Loc        int                    `json:"loc"`
	Functions  int                    `json:"functions"`
	PerFile    map[string]*fileMetric `json:"per_file"`
}

// compound reports whether a node opens a nesting level.
func compound(n ast.Node) bool {
	switch n.(type) {
	case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt,
		*ast.TypeSwitchStmt, *ast.SelectStmt, *ast.CaseClause, *ast.CommClause:
		return true
	}
	return false
}

// decisions counts the decision points inside one function body: one per
// branch, loop or case, plus one per short-circuit operator.
func decisions(body ast.Node) int {
	n := 0
	ast.Inspect(body, func(node ast.Node) bool {
		switch s := node.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt,
			*ast.TypeSwitchStmt, *ast.SelectStmt, *ast.CaseClause, *ast.CommClause:
			n++
		case *ast.BinaryExpr:
			// && and || each add a path; == and + do not.
			if s.Op == token.LAND || s.Op == token.LOR {
				n++
			}
		}
		return true
	})
	return n
}

// nesting is the deepest compound-statement depth in one function body, counting
// the body's own block as level one, so a flat function returns one.
//
// One recursion over the AST: enter a node at the depth of its parent's block,
// add one if the node itself opens a block, then recurse into the node's children
// by walking the fields ast.Walk would. It does not use ast.Inspect, because a
// descent that both recurses and asks Inspect to descend walks the same subtree
// twice, and the two walks multiply.
func nesting(body ast.Node) int {
	var deepest int
	var walk func(n ast.Node, d int)
	walk = func(n ast.Node, d int) {
		here := d
		if compound(n) {
			here = d + 1
			if here > deepest {
				deepest = here
			}
		}
		for _, child := range childrenOf(n) {
			walk(child, here)
		}
	}
	walk(body, 1)
	return deepest
}

// childrenOf is the direct children of a node, which is what ast.Walk visits. It
// is written out rather than taken from the reflect-based walker because the
// calls that matter here are few and fixed, and an incomplete list shows up as a
// low depth rather than a crash.
func childrenOf(n ast.Node) []ast.Node {
	var out []ast.Node
	add := func(items ...ast.Node) {
		for _, it := range items {
			if it != nil {
				out = append(out, it)
			}
		}
	}
	switch s := n.(type) {
	case *ast.BlockStmt:
		for _, st := range s.List {
			add(st)
		}
	case *ast.IfStmt:
		add(s.Init, s.Cond, s.Body, s.Else)
	case *ast.ForStmt:
		add(s.Init, s.Cond, s.Post, s.Body)
	case *ast.RangeStmt:
		add(s.Key, s.Value, s.X, s.Body)
	case *ast.SwitchStmt:
		add(s.Init, s.Tag, s.Body)
	case *ast.TypeSwitchStmt:
		add(s.Init, s.Assign, s.Body)
	case *ast.CaseClause:
		for _, e := range s.List {
			add(e)
		}
		for _, st := range s.Body {
			add(st)
		}
	case *ast.CommClause:
		add(s.Comm)
		for _, st := range s.Body {
			add(st)
		}
	case *ast.SelectStmt:
		add(s.Body)
	case *ast.LabeledStmt:
		add(s.Stmt)
	case *ast.FuncDecl:
		add(s.Body)
	}
	return out
}

// locLines is the count of physical lines that are neither blank nor
// comment-only.
func locLines(src string) int {
	n := 0
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") {
			continue
		}
		n++
	}
	return n
}

func fileOf(path string) *fileMetric {
	fm := &fileMetric{}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		fm.Err = err.Error()
		return fm
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		fm.Functions++
		fm.Complexity += 1 + decisions(fn.Body)
		if n := nesting(fn.Body); n > fm.MaxNesting {
			fm.MaxNesting = n
		}
	}
	if src, err := os.ReadFile(path); err == nil {
		fm.Loc = locLines(string(src))
	}
	return fm
}
