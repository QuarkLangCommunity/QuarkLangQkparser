package qkparser

// preproc.go — the line-level **preprocessor**, the runtime half of the standard (§7.2: "runtime = the
// preprocessor directives"): it decides which lines a program is, before any lexer sees them.
//
// It lives in the syntax layer because BOTH engines must apply the same one. The interpreter and the
// compiler each used to own half of it — the compiler this whole file, the interpreter nothing — so a
// program guarded by `#ifdef` ran in one engine and was a ParseError in the other. There is no mode
// parameter and no per-engine branch here: the engines differ only in which symbols they predefine
// (`COMPILE` / `EXPLAIN`), which is a property of the caller, not of the directive.
//
// Supported directives:
//   #define NAME                 define a feature switch
//   #undef NAME
//   #ifdef NAME / #ifndef NAME
//   #if <cond> / #elif <cond> / #else / #endif
//   #include "path.qk"           relative to the current file
//   #error <msg>                error out when the condition is not met
//
// Conditional expressions:
//   os("linux"|"darwin"|"windows")   arch("x86_64"|"arm64"|"any")
//   defined(NAME)  true  false  !  &&  ||   parentheses
//
// The target platform defaults to the host; a caller cross-preprocessing (the compiler's --target-os /
// --target-arch) sets OS / Arch before processing.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Context is one preprocessing context: the target platform, the defined symbols, and the `#include`
// depth. It is stateful on purpose — `#define` in one file is visible in the next `Process` call and in
// every file pulled in through `#include`, which is what makes a feature switch a property of a program
// rather than of a single file.
type Context struct {
	OS      string          // "linux" / "darwin" / "windows"; the host when empty
	Arch    string          // "x86_64" / "arm64"; the host when empty
	Defines map[string]bool // the defined symbols; a predefined engine symbol starts here
	// Aligned keeps the line numbering: a line the preprocessor consumes contributes an empty line
	// instead of disappearing, so a reader of the result (a linter, a language server, a doc renderer)
	// can keep reporting positions in the file the author wrote without carrying a line map. The engines
	// leave it off: they want the program, not a position-preserving view of it.
	Aligned bool
	depth   int
}

// NewContext builds a preprocessing context for the host platform with the given symbols already
// defined — the engine's own name for itself (`COMPILE` in the compiler, `EXPLAIN` in the interpreter),
// which is how `#ifdef` selects between the two engines without either engine branching in code.
func NewContext(defines ...string) *Context {
	c := &Context{OS: HostOS(), Arch: HostArch(), Defines: map[string]bool{}}
	for _, d := range defines {
		c.Defines[d] = true
	}
	return c
}

// LineMap records where each line of a preprocessed result came from: Original[i] is the 1-based line of
// the file Process was given that produced output line i+1, and 0 marks a line the preprocessor itself
// contributed (the text spliced in for an #include). It exists because preprocessing changes line
// numbers, and a diagnostic, a source map or a breakpoint has to name the line the author wrote.
type LineMap struct {
	Original []int
}

// SourceLine converts a line of preprocessed text back to the line of the file it was written in; the
// second result is false when the line has no source (it came from an #include).
func (m LineMap) SourceLine(line int) (int, bool) {
	if line < 1 || line > len(m.Original) {
		return 0, false
	}
	if m.Original[line-1] == 0 {
		return 0, false
	}
	return m.Original[line-1], true
}

// Result is one preprocessed file: the surviving text and the map back to its lines.
type Result struct {
	Text string
	Map  LineMap
}

// Process preprocesses one source file and returns the lines that survive; the filename is used for
// `#include` resolution and for the position in every diagnostic.
func (c *Context) Process(src, filename string) (string, error) {
	r, err := c.ProcessMapped(src, filename)
	return r.Text, err
}

// ProcessMapped is Process plus the map from the result's lines back to the lines of src — what a caller
// needs to keep reporting positions in the file the author wrote.
func (c *Context) ProcessMapped(src, filename string) (Result, error) {
	c.applyHostDefaults()
	if c.depth > 16 {
		return Result{}, errors.New(msgID(IDPpIncludeTooDeep, filename))
	}
	var out sourceText
	out.srcLines = len(strings.Split(src, "\n"))
	out.aligned = c.Aligned
	lines := strings.Split(src, "\n")
	// active: whether the current branch emits; taken: whether a branch in this group already matched
	type frame struct{ active, taken, parentActive bool }
	stack := []frame{}
	active := true
	for i, ln := range lines {
		if m := reIfLine.FindStringSubmatch(ln); m != nil {
			kind, expr := m[1], m[2]
			cond := false
			if active || len(stack) > 0 {
				c0, err := c.eval(expr)
				if err != nil {
					return Result{}, fmt.Errorf("%s:%d: %v", filename, i+1, err)
				}
				cond = c0
			}
			if kind == "if" {
				stack = append(stack, frame{active: false, taken: false, parentActive: active})
				top := &stack[len(stack)-1]
				top.active = top.parentActive && cond
				top.taken = cond
				active = top.active
			} else { // elif
				if len(stack) == 0 {
					return Result{}, errors.New(msgID(IDPpElifWithoutIf, filename, i+1))
				}
				top := &stack[len(stack)-1]
				top.active = top.parentActive && !top.taken && cond
				top.taken = top.taken || cond
				active = top.active
			}
			out.consume()
			continue
		}
		if m := reDirLine.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			switch m[1] {
			case "else", "endif":
				if len(stack) == 0 {
					return Result{}, errors.New(msgID(IDPpBranchWithoutIf, filename, i+1, m[1]))
				}
				// The frame is read before the pop: a pointer into the stack outlives its element, and
				// reading it after the shrink reports whatever the next push writes there.
				// Closed group: what is active is what enclosed it, not what the group selected.
				f := stack[len(stack)-1]
				if m[1] == "else" {
					f.active = f.parentActive && !f.taken
					f.taken = true
					stack[len(stack)-1] = f
					active = f.active
				} else {
					stack = stack[:len(stack)-1]
					active = f.parentActive
				}
				out.consume()
				continue
			case "define":
				if active {
					if nm := strings.TrimSpace(m[2]); reDefineNm.MatchString(nm) {
						c.Defines[nm] = true
					}
				}
				out.consume()
				continue
			case "undef":
				if active {
					delete(c.Defines, strings.TrimSpace(m[2]))
				}
				out.consume()
				continue
			case "ifdef", "ifndef":
				cond := c.Defines[strings.TrimSpace(m[2])]
				if m[1] == "ifndef" {
					cond = !cond
				}
				stack = append(stack, frame{active: active && cond, taken: cond, parentActive: active})
				active = stack[len(stack)-1].active
				out.consume()
				continue
			case "error":
				if active {
					return Result{}, fmt.Errorf("%s:%d: #error %s", filename, i+1, strings.TrimSpace(m[2]))
				}
				out.consume()
				continue
			}
		}
		if m := reInclude.FindStringSubmatch(ln); m != nil {
			if !active {
				out.consume()
				continue
			}
			inc := c.includePath(filename, m[1])
			b, err := os.ReadFile(inc)
			if err != nil {
				return Result{}, errors.New(msgID(IDPpIncludeFailed, filename, i+1, err))
			}
			c.depth++
			sub, err := c.ProcessMapped(string(b), inc)
			c.depth--
			if err != nil {
				return Result{}, err
			}
			// The included text replaces the directive line: it is spliced in whole (its own trailing
			// newline included), so the lines around it keep their content and the line count grows by
			// exactly the included file's line count. Its lines have no line in this file.
			out.add(sub.Text)
			continue
		}
		if active {
			out.keep(ln, i+1)
		} else {
			out.consume()
		}
	}
	if len(stack) != 0 {
		return Result{}, errors.New(msgID(IDPpUnclosedIf, filename, len(stack)))
	}
	return Result{Text: out.String(), Map: out.LineMap()}, nil
}

// sourceText accumulates a preprocessed file and, for every line of the result, the line of the input it
// came from — without the second half a diagnostic after a directive would name a line that does not
// exist in the file the author wrote.
//
// It reproduces strings.Join(out, "\n") exactly: a line the preprocessor consumed contributes nothing
// (the lines around it join as if it were absent), and a line that was kept contributes itself — the
// split's trailing empty element included, so a file ending in a newline still ends in one.
type sourceText struct {
	lines    []string
	origins  []int
	aligned  bool
	srcLines int  // how many lines the input has, so the trailing empty element is recognised
	trailing bool // the input ended in a newline, so the result has to as well
}

// keep adds one surviving input line together with the line it came from; the empty last element a
// trailing newline produces is not a line, and only the join may write it back.
func (s *sourceText) keep(text string, line int) {
	if text == "" && line == s.srcLines && !s.aligned {
		s.trailing = true
		return
	}
	s.lines = append(s.lines, text)
	s.origins = append(s.origins, line)
}

// consume takes one line the preprocessor decided not to emit: it contributes nothing unless the caller
// asked for aligned output, in which case it contributes its (empty) line.
func (s *sourceText) consume() {
	if s.aligned {
		s.lines = append(s.lines, "")
		s.origins = append(s.origins, 0)
	}
}

// add splices text that came from no line of the input (an #include): it joins it as one more element of
// the result, contributing one result line per line of that text and no line in this file.
func (s *sourceText) add(text string) {
	s.lines = append(s.lines, text)
	for i := 0; i < len(strings.Split(text, "\n")); i++ {
		s.origins = append(s.origins, 0)
	}
	s.trailing = strings.HasSuffix(text, "\n")
}

// String is the preprocessed text: the surviving lines joined with newlines, keeping the trailing empty
// element the input's own split produced when it ended in a newline.
func (s *sourceText) String() string {
	lines := s.lines
	if s.trailing {
		lines = append(append([]string{}, lines...), "")
	}
	return strings.Join(lines, "\n")
}

// LineMap is where each line of the result came from.
func (s *sourceText) LineMap() LineMap {
	n := len(strings.Split(s.String(), "\n"))
	origins := s.origins
	for len(origins) < n {
		origins = append(origins, 0)
	}
	if len(origins) > n {
		origins = origins[:n]
	}
	return LineMap{Original: origins}
}

// includePath resolves an #include target against the including file's directory.
func (c *Context) includePath(filename, target string) string {
	if target == "" {
		return target
	}
	return filepath.Join(filepath.Dir(filename), target)
}

// applyHostDefaults fills OS and Arch from the host when the caller left them empty.
func (c *Context) applyHostDefaults() {
	if c.OS == "" {
		c.OS = HostOS()
	}
	if c.Arch == "" {
		c.Arch = HostArch()
	}
	if c.Defines == nil {
		c.Defines = map[string]bool{}
	}
}

// Process preprocesses one source file for the host platform with the given symbols defined — the
// shorthand for callers that keep no context and pull in no further files.
func Process(src, filename string, defines ...string) (string, error) {
	return NewContext(defines...).Process(src, filename)
}

// HostOS is the host platform in the preprocessor's own spelling (`runtime.GOOS` is the same string).
func HostOS() string { return runtime.GOOS }

// HostArch is the host architecture in the preprocessor's spelling: Go names the 64-bit x86 port `amd64`
// while `arch("x86_64")` is what a program writes.
func HostArch() string {
	if runtime.GOARCH == "amd64" {
		return "x86_64"
	}
	return runtime.GOARCH
}

var (
	reIfLine   = regexp.MustCompile(`^\s*#\s*(if|elif)\s+(.*)$`)
	reDirLine  = regexp.MustCompile(`^\s*#\s*([a-z_]+)\b\s*(.*)$`)
	reInclude  = regexp.MustCompile(`^\s*#\s*include\s+"([^"]+)"\s*$`)
	reDefineNm = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	reOsArg    = regexp.MustCompile(`^os\s*\(\s*"([^"]*)"\s*\)$`)
	reArchArg  = regexp.MustCompile(`^arch\s*\(\s*"([^"]*)"\s*\)$`)
	reDefined  = regexp.MustCompile(`^defined\s*\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)$`)
)

// eval evaluates a conditional expression.
func (c *Context) eval(expr string) (bool, error) {
	v, err := c.evalOr(strings.TrimSpace(expr))
	return v, err
}

func (c *Context) evalOr(e string) (bool, error) {
	parts := splitTopExpr(e, "||")
	if len(parts) > 1 {
		for _, p0 := range parts {
			if v, err := c.evalAnd(strings.TrimSpace(p0)); err != nil {
				return false, err
			} else if v {
				return true, nil
			}
		}
		return false, nil
	}
	return c.evalAnd(e)
}

func (c *Context) evalAnd(e string) (bool, error) {
	parts := splitTopExpr(e, "&&")
	if len(parts) > 1 {
		for _, p0 := range parts {
			if v, err := c.evalUnary(strings.TrimSpace(p0)); err != nil {
				return false, err
			} else if !v {
				return false, nil
			}
		}
		return true, nil
	}
	return c.evalUnary(e)
}

func (c *Context) evalUnary(e string) (bool, error) {
	e = strings.TrimSpace(e)
	if strings.HasPrefix(e, "!") {
		v, err := c.evalUnary(strings.TrimSpace(e[1:]))
		return !v, err
	}
	if strings.HasPrefix(e, "(") && strings.HasSuffix(e, ")") {
		return c.evalOr(strings.TrimSpace(e[1 : len(e)-1]))
	}
	switch e {
	case "true", "1":
		return true, nil
	case "false", "0", "":
		return false, nil
	}
	if m := reOsArg.FindStringSubmatch(e); m != nil {
		return m[1] == "any" || m[1] == c.OS, nil
	}
	if m := reArchArg.FindStringSubmatch(e); m != nil {
		return m[1] == "any" || m[1] == c.Arch, nil
	}
	if m := reDefined.FindStringSubmatch(e); m != nil {
		return c.Defines[m[1]], nil
	}
	if reDefineNm.MatchString(e) {
		return c.Defines[e], nil
	}
	return false, errors.New(msgID(IDPpUnrecognizedCondition, e))
}

// splitTopExpr splits on top-level operators (ignoring what is inside parentheses).
func splitTopExpr(s, op string) []string {
	var out []string
	depth, last := 0, 0
	for i := 0; i+len(op) <= len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth == 0 && strings.HasPrefix(s[i:], op) {
			out = append(out, s[last:i])
			last = i + len(op)
			i += len(op) - 1
		}
	}
	out = append(out, s[last:])
	return out
}
