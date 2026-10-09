# QuarkLangQkparser

The QuarkLang **syntax layer**, extracted from the language repository so that modules outside
`github.com/QuarkLangCommunity/QuarkLangQkc/` can parse QuarkLang source.

```go
import "github.com/QuarkLangCommunity/QuarkLangQkparser"
```

## What is in here

| File | Contents |
| --- | --- |
| `token.go` | The lexer: `TokenKind`, `Token`, `Comment`, `Lex`, `LexWithComments` |
| `ast.go` | The AST: `Pos`, `Program`, the `Stmt`/`Expr` node types, `CallExpr.PlainArgs` |
| `parser.go` | `Parse` and the recursive-descent parser behind it; `ParseError` |
| `macro.go` | The token-level macro system: `SplitMacroDefs`, `ExpandMacros` |
| `refperm.go` | The parser half of permission-carrying reference types (`&rw u int p`) |
| `frontend.go` | Single-file entry points: `ParseSource`, `ParseSourceWithComments`, `ParseSourceAll` |
| `localizer.go` | The message seam: `Localizer`, `SetLocalizer` |

## What is deliberately *not* in here

Type checking, the linter, documentation rendering, the evaluator/VM, FFI and the i18n catalog all
stay in the language repository. This package imports **nothing but the Go standard library** and has
no dependency on the language repository, so there is no import cycle in either direction.

## Usage

```go
prog, err := qkparser.ParseSource(src)
if err != nil {
    return err
}
for _, fn := range prog.Funcs {
    fmt.Println(fn.Name)
}
```

`ParseSourceAll` additionally returns the comments and the `#macro` definitions, which are cut out of
the token stream before parsing and are therefore not part of the AST.

## Messages

Error and diagnostic templates are the keys of the language repository's message catalog, and this
package never reaches for that catalog itself. Instead it renders every message through an installed
`Localizer`, whose default (`verbatim`) returns templates unchanged:

```go
qkparser.SetLocalizer(myCatalog) // returns the previous localizer
```

An embedder that wants translated messages installs its own localizer; one that does not gets the
templates as written.

## License

MIT — see [LICENSE](LICENSE).
