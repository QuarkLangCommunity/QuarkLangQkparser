package qkparser

import (
	"strings"
	"testing"
)

const sampleSrc = `#macro twice (a) {
    #return #insert(#ast(a)) + #insert(#ast(a))
}

// main drives the sample.
fn main(IOStream io) void {
    io.println(twice(21));
}

fn helper(int n) int {
    return n + 1;
}
`

// functionNames returns the top-level function names of a program in declaration order.
func functionNames(prog *Program) []string {
	names := make([]string, 0, len(prog.Funcs))
	for _, f := range prog.Funcs {
		names = append(names, f.Name)
	}
	return names
}

// prefixMessages renders every message with a fixed prefix, which makes the seam observable: the ID
// decides which message it is, and the installed localizer decides how it reads.
type prefixMessages struct{ prefix string }

// Msg renders the message named by id, with its arguments, behind the prefix.
func (p prefixMessages) Msg(id ID, args ...any) string { return p.prefix + RenderSource(id, args...) }

// TestParseSourceCollectsFunctionNames: the frontend entry point returns the parsed functions, which
// is what a tool needs to list them.
func TestParseSourceCollectsFunctionNames(t *testing.T) {
	prog, err := ParseSource(sampleSrc)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(functionNames(prog), ",")
	if want := "main,helper"; got != want {
		t.Fatalf("function names = %q, want %q", got, want)
	}
	if prog.Funcs[1].Ret != "int" || len(prog.Funcs[1].Params) != 1 {
		t.Fatalf("helper signature not parsed: %+v", prog.Funcs[1])
	}
}

// TestParseSourceAllReturnsMacrosAndComments: macro definitions are cut out before parsing, so the
// frontend has to hand them back separately for documentation tools.
func TestParseSourceAllReturnsMacrosAndComments(t *testing.T) {
	prog, comments, macros, err := ParseSourceAll(sampleSrc)
	if err != nil {
		t.Fatal(err)
	}
	if len(macros) != 1 || macros[0].Name != "twice" || len(macros[0].Params) != 1 {
		t.Fatalf("macro definitions = %+v", macros)
	}
	if len(comments) != 1 || !strings.Contains(comments[0].Text, "main drives") {
		t.Fatalf("comments = %+v", comments)
	}
	if len(prog.Funcs) != 2 {
		t.Fatalf("the macro definition leaked into the AST: %d functions", len(prog.Funcs))
	}
}

// TestParseSourceExpandsMacroCalls: a macro call is expanded at token level, so the AST of main holds
// the substituted expression rather than a call to the macro.
func TestParseSourceExpandsMacroCalls(t *testing.T) {
	prog, err := ParseSource(sampleSrc)
	if err != nil {
		t.Fatal(err)
	}
	body := prog.Funcs[0].Body
	if len(body.Stmts) != 1 {
		t.Fatalf("main body has %d statements", len(body.Stmts))
	}
	stmt, ok := body.Stmts[0].(*ExprStmt)
	if !ok {
		t.Fatalf("statement type = %T, want *ExprStmt", body.Stmts[0])
	}
	call, ok := stmt.X.(*CallExpr)
	if !ok || len(call.Args) != 1 {
		t.Fatalf("io.println call not parsed: %#v", stmt.X)
	}
	if _, isBinOp := call.Args[0].(*BinOp); !isBinOp {
		t.Fatalf("macro call survived expansion: %#v", call.Args[0])
	}
}

// TestParseSourceKeepsFileCoordinates: every frontend entry point reports original file positions,
// because tools map diagnostics straight back onto the source.
func TestParseSourceKeepsFileCoordinates(t *testing.T) {
	prog, err := ParseSource(sampleSrc)
	if err != nil {
		t.Fatal(err)
	}
	if line := prog.Funcs[0].Pos.Line; line != 6 {
		t.Fatalf("main is declared on line 6, got %d", line)
	}
	if line := prog.Funcs[1].Pos.Line; line != 10 {
		t.Fatalf("helper is declared on line 10, got %d", line)
	}
}

// TestParseErrorCarriesPosition: a syntax error is a *ParseError carrying the line and column of the
// offending token.
func TestParseErrorCarriesPosition(t *testing.T) {
	_, err := ParseSource("fn main(IOStream io) void {\n    int x = ;\n}\n")
	if err == nil {
		t.Fatal("expected a parse error")
	}
	perr, ok := err.(*ParseError)
	if !ok {
		t.Fatalf("error type = %T, want *ParseError", err)
	}
	if perr.Line != 2 || perr.Col != 13 {
		t.Fatalf("error position = line %d col %d, want line 2 col 13", perr.Line, perr.Col)
	}
}

// TestSetLocalizerRoutesMessages: the package renders messages through the installed localizer, so an
// embedder can translate them without this package depending on a catalog.
func TestSetLocalizerRoutesMessages(t *testing.T) {
	prev := SetLocalizer(prefixMessages{prefix: "[probe] "})
	defer SetLocalizer(prev)
	_, err := ParseSource("fn main(IOStream io) void {\n    #macro\n}\n")
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if !strings.Contains(err.Error(), "[probe] ") {
		t.Fatalf("the installed localizer was not used: %v", err)
	}
}

// TestSourceLocalizerRendersTheAuthoredEnglish: with no localizer installed the package renders its
// own authored English text — the source text of the ID, out of the packed blob.
func TestSourceLocalizerRendersTheAuthoredEnglish(t *testing.T) {
	if got := (sourceLocalizer{}).Msg(IDNestingTooDeep, 7); !strings.Contains(got, "7 levels") {
		t.Fatalf("source localizer rendered %q", got)
	}
	if got := (sourceLocalizer{}).Msg(IDPpInsertNeedsAst); got != "#insert 需要 (#ast(名字)) 形式" {
		t.Fatalf("source localizer rendered %q", got)
	}
}

// TestLexSeparatesCommentsFromTokens: lexing with comments returns both streams so a formatter can
// put the comments back.
func TestLexSeparatesCommentsFromTokens(t *testing.T) {
	toks, comments, err := LexWithComments("// lead\nint x = 1; /* tail */\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 2 {
		t.Fatalf("comments = %+v", comments)
	}
	if comments[0].Block || !comments[1].Block {
		t.Fatalf("comment kinds not preserved: %+v", comments)
	}
	seen := false
	for _, tk := range toks {
		if tk.Kind == TIdent && tk.Text == "x" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("the identifier token is missing from the stream")
	}
}
