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

// Process preprocesses one source file and returns the lines that survive; the filename is used for
// `#include` resolution and for the position in every diagnostic.
func (c *Context) Process(src, filename string) (string, error) {
	c.applyHostDefaults()
	if c.depth > 16 {
		return "", errors.New(msg("预处理 #include 层数过深（>16）：%s", filename))
	}
	lines := strings.Split(src, "\n")
	var out []string
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
					return "", fmt.Errorf("%s:%d: %v", filename, i+1, err)
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
					return "", errors.New(msg("%s:%d: #elif 没有对应的 #if", filename, i+1))
				}
				top := &stack[len(stack)-1]
				top.active = top.parentActive && !top.taken && cond
				top.taken = top.taken || cond
				active = top.active
			}
			continue
		}
		if m := reDirLine.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			switch m[1] {
			case "else":
				if len(stack) == 0 {
					return "", errors.New(msg("%s:%d: #else 没有对应的 #if", filename, i+1))
				}
				top := &stack[len(stack)-1]
				top.active = top.parentActive && !top.taken
				top.taken = true
				active = top.active
				continue
			case "endif":
				if len(stack) == 0 {
					return "", errors.New(msg("%s:%d: #endif 没有对应的 #if", filename, i+1))
				}
				f := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				active = f.parentActive
				continue
			case "define":
				if active {
					if nm := strings.TrimSpace(m[2]); reDefineNm.MatchString(nm) {
						c.Defines[nm] = true
					}
				}
				continue
			case "undef":
				if active {
					delete(c.Defines, strings.TrimSpace(m[2]))
				}
				continue
			case "ifdef", "ifndef":
				nm := strings.TrimSpace(m[2])
				cond := c.Defines[nm]
				if m[1] == "ifndef" {
					cond = !cond
				}
				stack = append(stack, frame{active: active && cond, taken: cond, parentActive: active})
				active = stack[len(stack)-1].active
				continue
			case "error":
				if active {
					return "", fmt.Errorf("%s:%d: #error %s", filename, i+1, strings.TrimSpace(m[2]))
				}
				continue
			}
		}
		if m := reInclude.FindStringSubmatch(ln); m != nil {
			if !active {
				continue
			}
			inc := c.includePath(filename, m[1])
			b, err := os.ReadFile(inc)
			if err != nil {
				return "", errors.New(msg("%s:%d: #include 失败：%v", filename, i+1, err))
			}
			c.depth++
			sub, err := c.Process(string(b), inc)
			c.depth--
			if err != nil {
				return "", err
			}
			// The included text replaces the directive line: it is spliced in whole (its own trailing
			// newline included), so the lines around it keep their content and the line count grows by
			// exactly the included file's line count.
			if len(out) == 0 {
				out = append(out, sub)
			} else if strings.HasSuffix(out[len(out)-1], "\n") {
				out = append(out, sub, "")
			} else {
				out = append(out, "\n"+sub)
			}
			continue
		}
		if active {
			out = append(out, ln)
		}
	}
	if len(stack) != 0 {
		return "", errors.New(msg("%s: 有 %d 个 #if 未闭合（缺 #endif）", filename, len(stack)))
	}
	return strings.Join(out, "\n"), nil
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
	return false, errors.New(msg("无法识别的条件表达式：%q", e))
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
