package qkparser

import (
	"strconv"
	"testing"
)

// exprShape renders an expression fully parenthesized, so one string shows the grouping the parser chose.
func exprShape(e Expr) string {
	switch x := e.(type) {
	case *BinOp:
		return "(" + exprShape(x.L) + " " + x.Op + " " + exprShape(x.R) + ")"
	case *UnOp:
		return "(" + x.Op + exprShape(x.X) + ")"
	case *IntLit:
		return strconv.FormatInt(x.V, 10)
	case *Ident:
		return x.Name
	case *CallExpr:
		shapes := make([]string, 0, len(x.Args))
		for _, a := range x.Args {
			shapes = append(shapes, exprShape(a))
		}
		return exprShape(x.Fn) + "(" + joinComma(shapes) + ")"
	case *IndexExpr:
		return exprShape(x.X) + "[" + exprShape(x.Idx) + "]"
	}
	return "?"
}

// joinComma joins rendered argument shapes, because this test needs a rendering and not a formatter.
func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// parseInit parses one declaration and returns the shape of its initializer.
func parseInit(t *testing.T, expr string) string {
	t.Helper()
	prog, err := ParseSource("fn main(IOStream io) void {\n    int v = " + expr + ";\n}\n")
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	if len(prog.Funcs) != 1 || len(prog.Funcs[0].Body.Stmts) != 1 {
		t.Fatalf("%s: parsed %d functions with %d statements, want 1 and 1", expr, len(prog.Funcs), len(prog.Funcs[0].Body.Stmts))
	}
	decl, ok := prog.Funcs[0].Body.Stmts[0].(*DeclStmt)
	if !ok {
		t.Fatalf("%s: statement type = %T, want *DeclStmt", expr, prog.Funcs[0].Body.Stmts[0])
	}
	return exprShape(decl.Init)
}

// TestPrecedenceTableMatchesTheSpec pins the whole precedence ladder of the language spec, one adjacency
// per row, because the parity gate between the two engines lives in another repository and cannot see a
// precedence disagreement — both engines can hold the same wrong order. The grouping is decided here and
// nowhere else: parsePrimary's TLParen case erases the parenthesis node and returns the inner expression,
// so a later pass receives a tree that has already grouped the operands. The shift rows are the ones this
// module had inverted: `<<`/`>>` is looser than `+`/`-`, so `a + b << c` reads `(a + b) << c`.
func TestPrecedenceTableMatchesTheSpec(t *testing.T) {
	cases := []struct{ expr, want string }{
		// Loosest first: `||`, `&&`, `== != < <= > >=`, `|`, `^`, `&`, `<< >>`, `+ -`, `* / %`, unary.
		{"a || b && c", "(a || (b && c))"},
		{"a && b == c", "(a && (b == c))"},
		// The comparison family is one level of the ladder, so it groups left to right.
		{"a == b < c", "((a == b) < c)"},
		{"a == b | c", "(a == (b | c))"},
		{"a | b ^ c", "(a | (b ^ c))"},
		{"a ^ b & c", "(a ^ (b & c))"},
		{"a & b << c", "(a & (b << c))"},
		{"a + b << c", "((a + b) << c)"},
		{"a << b + c", "(a << (b + c))"},
		{"a - b >> c", "((a - b) >> c)"},
		{"a >> b - c", "(a >> (b - c))"},
		{"a + b * c", "(a + (b * c))"},
		{"a * b % c", "((a * b) % c)"},
		{"a * -b", "(a * (-b))"},
		{"-a + b", "((-a) + b)"},
		{"~a & b", "((~a) & b)"},
		// One level groups left to right.
		{"a - b - c", "((a - b) - c)"},
		{"a << b << c", "((a << b) << c)"},
		{"a / b * c", "((a / b) * c)"},
		// An explicit parenthesis survives as grouping although its node is erased: the two readings the
		// module used to conflate now have two shapes, and `a + b << c` agrees with `(a + b) << c`.
		{"a + (b << c)", "(a + (b << c))"},
		{"(a + b) << c", "((a + b) << c)"},
		{"a + b << c", "((a + b) << c)"},
		{"(a + b) * c", "((a + b) * c)"},
	}
	for _, tc := range cases {
		if got := parseInit(t, tc.expr); got != tc.want {
			t.Errorf("%s groups as %s, want %s", tc.expr, got, tc.want)
		}
	}
}

// TestShiftIsLooserThanAddition: the three expressions of the spec's §5 table keep their stated values,
// which is the property the shift level exists for.
func TestShiftIsLooserThanAddition(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"1 + 2 << 3", "((1 + 2) << 3)"},
		{"1 << 2 + 3", "(1 << (2 + 3))"},
		{"10 - 2 >> 1", "((10 - 2) >> 1)"},
		{"1 + (2 << 3)", "(1 + (2 << 3))"},
		{"(1 + 2) << 3", "((1 + 2) << 3)"},
	}
	for _, tc := range cases {
		if got := parseInit(t, tc.expr); got != tc.want {
			t.Errorf("%s groups as %s, want %s", tc.expr, got, tc.want)
		}
	}
}
