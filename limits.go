package qkparser

// MaxExprDepth is the nesting depth this package accepts in one expression or statement body. Both
// entry points of the recursive descent count their own levels against it — parseExpr for expressions
// and parseStmt for nested blocks, branches and loops — and each refuses the level past it with the
// position of the token it stopped on.
//
// It is an explicit counter, never a probe of the Go stack: a process stack is a platform-sized
// resource, so an implementation that let it decide would answer differently on linux, darwin and
// windows. The limit is deliberately far above anything a human writes — the largest expression in the
// QuarkLang repository's stress corpus is ten thousand chained calls — and far below the depth at which
// the descent, and the type checker that walks the same tree afterwards, runs the stack out (measured
// there: 290 000 nodes survive, 320 000 take the process down), so refusing is always possible.
//
// The interpreter and the compiler both parse through this package, so the number above and the wording
// of the refusal are defined once and reach both engines unchanged.
const MaxExprDepth = 65536
