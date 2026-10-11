package qkparser

// Regression tests for the parser's nesting limit (MaxExprDepth, see limits.go). A descent deeper than
// the limit used to exhaust the Go stack and take the whole process down — 300 000 nested parentheses
// did it — so the parser now counts its own levels and refuses with a positioned *ParseError. The
// interpreter and the compiler share this package, so one refusal text reaches both engines.

import (
	"errors"
	"strings"
	"testing"
)

// nestSource builds a program whose single call argument is n nested parentheses, on line 2.
func nestSource(n int) string {
	return "fn main(IOStream io) void {\n  io.println(" + strings.Repeat("(", n) + "1" + strings.Repeat(")", n) + ");\n}\n"
}

// TestOverDeepNestIsRefusedWithPosition: the nest the type checker never sees is refused by the descent
// that consumes it, with the line the nest is on.
func TestOverDeepNestIsRefusedWithPosition(t *testing.T) {
	_, err := ParseSource(nestSource(2 * MaxExprDepth))
	if err == nil {
		t.Fatal("a nest past MaxExprDepth must be refused")
	}
	var perr *ParseError
	if !errors.As(err, &perr) {
		t.Fatalf("error type = %T (%v), want *ParseError", err, err)
	}
	if perr.Line != 2 {
		t.Errorf("diagnostic line = %d, want 2 (the line the nest is on)", perr.Line)
	}
	if perr.Col < 1 {
		t.Errorf("diagnostic column = %d, want the position of the token the descent stopped on", perr.Col)
	}
	want := "nesting is deeper than 65536 levels, the limit of this implementation: split the expression into smaller statements"
	if !strings.Contains(perr.Msg, want) {
		t.Errorf("diagnostic = %q, want it to carry %q", perr.Msg, want)
	}
}

// TestNestUnderTheLimitParses: the limit refuses only what is over it, because a false refusal would
// break working programs. Ten thousand levels is the largest nest in the stress corpus.
func TestNestUnderTheLimitParses(t *testing.T) {
	if _, err := ParseSource(nestSource(10000)); err != nil {
		t.Fatalf("ten thousand nested parentheses must parse: %v", err)
	}
}

// TestDeepOperatorChainIsNotRefusedByTheParser pins the division of labour between the two guards: a
// left-deep chain of one operator is built by the precedence loop rather than by recursion, so the
// descent stays shallow and the parser accepts it — refusing that shape is the type checker's job, which
// walks the tree node by node afterwards.
func TestDeepOperatorChainIsNotRefusedByTheParser(t *testing.T) {
	var b strings.Builder
	b.WriteString("fn main(IOStream io) void {\n  io.println(0")
	for i := 0; i < 2*MaxExprDepth; i++ {
		b.WriteString("+1")
	}
	b.WriteString(");\n}\n")
	if _, err := ParseSource(b.String()); err != nil {
		t.Fatalf("the parser must accept a chain the type checker refuses: %v", err)
	}
}

// TestGuardRendersThroughTheInstalledLocalizer: the refusal is a message like any other in this package,
// so an embedder's localizer renders it — which is what keeps the interpreter's catalog covering it.
func TestGuardRendersThroughTheInstalledLocalizer(t *testing.T) {
	prev := SetLocalizer(prefixMessages{prefix: "[probe] "})
	defer SetLocalizer(prev)
	_, err := ParseSource(nestSource(2 * MaxExprDepth))
	if err == nil {
		t.Fatal("a nest past MaxExprDepth must be refused")
	}
	if !strings.Contains(err.Error(), "[probe] ") {
		t.Fatalf("the installed localizer was not used: %v", err)
	}
}

// TestEnterNestingRefusesExactlyTheLevelPastTheLimit pins the boundary of the shared counter: the level
// at the limit is accepted, the next one is refused, and a refused level is not left counted. Both
// guarded entry points (parseExpr and parseStmt) take their levels through this one function.
func TestEnterNestingRefusesExactlyTheLevelPastTheLimit(t *testing.T) {
	p := &parser{toks: []Token{{Kind: TEOF, Line: 9, Col: 4}}}
	p.depth = MaxExprDepth - 1
	if err := p.enterNesting(); err != nil {
		t.Fatalf("level %d must be accepted: %v", MaxExprDepth, err)
	}
	if p.depth != MaxExprDepth {
		t.Fatalf("depth after the accepted level = %d, want %d", p.depth, MaxExprDepth)
	}
	err := p.enterNesting()
	if err == nil {
		t.Fatalf("level %d must be refused", MaxExprDepth+1)
	}
	if p.depth != MaxExprDepth {
		t.Fatalf("depth after the refused level = %d, want %d (a refused level must not stay counted)", p.depth, MaxExprDepth)
	}
	var perr *ParseError
	if !errors.As(err, &perr) {
		t.Fatalf("error type = %T (%v), want *ParseError", err, err)
	}
	if perr.Line != 9 || perr.Col != 4 {
		t.Errorf("diagnostic position = line %d col %d, want line 9 col 4 (the current token)", perr.Line, perr.Col)
	}
}
