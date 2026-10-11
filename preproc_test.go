package qkparser

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPreprocPlatformSwitch: `#if os(...)` / `#elif` / `#else` select one branch for the target platform.
func TestPreprocPlatformSwitch(t *testing.T) {
	src := `#if os("windows")
io.println(3);
#elif os("darwin")
io.println(2);
#else
io.println(1);
#endif`
	linux := NewContext()
	linux.OS = "linux"
	out, err := linux.Process(src, "t.qk")
	if err != nil {
		t.Fatal(err)
	}
	if out != "io.println(1);" {
		t.Fatalf("linux 分支错误：%q", out)
	}
	win := NewContext()
	win.OS = "windows"
	out, err = win.Process(src, "t.qk")
	if err != nil {
		t.Fatal(err)
	}
	if out != "io.println(3);" {
		t.Fatalf("windows 分支错误：%q", out)
	}
}

// TestPreprocDefinesAndArch: `defined(NAME)`, a bare name, `arch(...)` and the `&&` / `||` / `!` operators.
func TestPreprocDefinesAndArch(t *testing.T) {
	src := `#define MARKET
#if arch("arm64") && defined(MARKET)
arm;
#endif
#if !defined(NOPE) || os("plan9")
yes;
#endif`
	ctx := NewContext()
	ctx.Arch = "arm64"
	out, err := ctx.Process(src, "t.qk")
	if err != nil {
		t.Fatal(err)
	}
	if out != "arm;\nyes;" {
		t.Fatalf("定义/架构条件错误：%q", out)
	}
}

// TestPreprocErrorDirective: `#error` fails on the branch that is taken and is silent on one that is not.
func TestPreprocErrorDirective(t *testing.T) {
	ctx := NewContext()
	ctx.OS = "windows"
	if _, err := ctx.Process("#if os(\"windows\")\n#error nope\n#endif", "t.qk"); err == nil {
		t.Fatal("#error 应触发失败")
	}
	ctx2 := NewContext()
	ctx2.OS = "linux"
	if _, err := ctx2.Process("#if os(\"windows\")\n#error nope\n#endif", "t.qk"); err != nil {
		t.Fatalf("非目标平台不应报错：%v", err)
	}
}

// TestPreprocUnclosed: an `#if` without its `#endif` is refused, not silently accepted.
func TestPreprocUnclosed(t *testing.T) {
	ctx := NewContext()
	if _, err := ctx.Process("#if os(\"linux\")\nx;\n", "t.qk"); err == nil {
		t.Fatal("未闭合的 #if 应报错")
	}
}

// TestPreprocEngineSymbol: the predefined engine symbol is what makes one source serve both engines —
// with COMPILE defined the compiler's branch survives and the interpreter's is dropped, and the other
// way round with EXPLAIN. Nothing else distinguishes the two runs.
func TestPreprocEngineSymbol(t *testing.T) {
	src := "#ifdef COMPILE\ncompiled;\n#endif\n#ifdef EXPLAIN\nexplained;\n#endif"
	compiled, err := Process(src, "t.qk", "COMPILE")
	if err != nil {
		t.Fatal(err)
	}
	if compiled != "compiled;" {
		t.Fatalf("COMPILE 分支错误：%q", compiled)
	}
	explained, err := Process(src, "t.qk", "EXPLAIN")
	if err != nil {
		t.Fatal(err)
	}
	if explained != "explained;" {
		t.Fatalf("EXPLAIN 分支错误：%q", explained)
	}
}

// TestPreprocIfdefInsideMacroBody: the macro facility's own directives (`#return`, `#error`, `#insert`)
// are token-level and unknown here, so they must survive preprocessing untouched — dropping them used to
// leave every guarded macro body empty. The conditional lines around them are still consumed.
func TestPreprocIfdefInsideMacroBody(t *testing.T) {
	src := "#macro add (a, b) {\n#ifdef COMPILE\n    #return a + b\n#endif\n}"
	out, err := Process(src, "t.qk", "COMPILE")
	if err != nil {
		t.Fatal(err)
	}
	if out != "#macro add (a, b) {\n    #return a + b\n}" {
		t.Fatalf("宏体内的预处理指令被改写：%q", out)
	}
	if _, err := Process(src, "t.qk"); err != nil {
		t.Fatalf("未定义 COMPILE 时不应报错：%v", err)
	}
}

// TestPreprocDefinePersistsAcrossFiles: a `#define` is a property of the program, so a switch defined in
// an included file is visible to the file that included it.
func TestPreprocDefinePersistsAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	inc := filepath.Join(dir, "switch.qk")
	if err := os.WriteFile(inc, []byte("#define MARKET\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.qk")
	ctx := NewContext()
	out, err := ctx.Process("#include \"switch.qk\"\n#ifdef MARKET\nkept;\n#endif\n", main)
	if err != nil {
		t.Fatal(err)
	}
	if out != "\nkept;\n" {
		t.Fatalf("#include 的定义没有生效：%q", out)
	}
}

// TestPreprocHostDefaults: an empty OS / Arch means the host, and the host's x86 spelling is x86_64.
func TestPreprocHostDefaults(t *testing.T) {
	ctx := NewContext()
	out, err := ctx.Process("#if arch(\"x86_64\") || arch(\"arm64\")\nhost;\n#endif", "t.qk")
	if err != nil {
		t.Fatal(err)
	}
	if out != "host;" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64") {
		t.Fatalf("宿主架构没有命中：%q", out)
	}
	if ctx.OS != runtime.GOOS || ctx.Arch != HostArch() {
		t.Fatalf("宿主默认值错误：%q / %q", ctx.OS, ctx.Arch)
	}
}

// TestPreprocUnknownDirectiveSurvives: a directive this stage does not know is left for the stage that
// does know it — silently deleting it would change the program rather than report it.
func TestPreprocUnknownDirectiveSurvives(t *testing.T) {
	out, err := Process("#run main\nint x = 1;\n", "t.qk")
	if err != nil {
		t.Fatal(err)
	}
	if out != "#run main\nint x = 1;\n" {
		t.Fatalf("未知指令被丢弃：%q", out)
	}
}

// TestPreprocUnbalancedDirectives: `#else` / `#elif` / `#endif` without an open `#if` are refused.
func TestPreprocUnbalancedDirectives(t *testing.T) {
	for _, src := range []string{"#else\nx;\n", "#elif os(\"linux\")\nx;\n", "#endif\nx;\n"} {
		if _, err := Process(src, "t.qk"); err == nil {
			t.Fatalf("孤立的指令应报错：%q", src)
		}
	}
}

// TestPreprocNestedConditionals: only a branch whose every enclosing branch is taken is emitted.
func TestPreprocNestedConditionals(t *testing.T) {
	src := "#ifdef OUTER\n#ifdef INNER\ndeep;\n#endif\n#endif\nafter;"
	out, err := Process(src, "t.qk", "OUTER")
	if err != nil {
		t.Fatal(err)
	}
	if out != "after;" {
		t.Fatalf("嵌套条件错误：%q", out)
	}
	out, err = Process(src, "t.qk", "OUTER", "INNER")
	if err != nil {
		t.Fatal(err)
	}
	if out != "deep;\nafter;" {
		t.Fatalf("嵌套条件错误：%q", out)
	}
}

// TestPreprocUndef: `#undef` removes a symbol, including one the engine predefined.
func TestPreprocUndef(t *testing.T) {
	out, err := Process("#undef EXPLAIN\n#ifdef EXPLAIN\nno;\n#endif\nyes;", "t.qk", "EXPLAIN")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no;") || out != "yes;" {
		t.Fatalf("#undef 没有生效：%q", out)
	}
}

// TestPreprocAlignedKeepsLineNumbers: a caller that reports positions in the file the author wrote — the
// linter, the language server, the doc renderer — reads the program with the consumed lines kept as empty
// ones, so line n of the result is line n of the input and no line map is needed.
func TestPreprocAlignedKeepsLineNumbers(t *testing.T) {
	src := "#ifdef EXPLAIN\nint x = 1;\n#endif\nint y = 2;\nint z = 3;\n"
	ctx := NewContext("EXPLAIN")
	ctx.Aligned = true
	out, err := ctx.Process(src, "t.qk")
	if err != nil {
		t.Fatal(err)
	}
	want := "\nint x = 1;\n\nint y = 2;\nint z = 3;\n"
	if out != want {
		t.Fatalf("对齐模式错误：%q，期望 %q", out, want)
	}
	if strings.Count(out, "\n") != strings.Count(src, "\n") {
		t.Fatalf("行数没有保持：%d vs %d", strings.Count(out, "\n"), strings.Count(src, "\n"))
	}
}

// TestPreprocAlignedDropsTheOtherBranch: alignment preserves the line count, not the content of a branch
// that was not taken.
func TestPreprocAlignedDropsTheOtherBranch(t *testing.T) {
	ctx := NewContext("EXPLAIN")
	ctx.Aligned = true
	out, err := ctx.Process("#ifdef COMPILE\ncompiled;\n#endif\nkept;\n", "t.qk")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "compiled") || out != "\n\n\nkept;\n" {
		t.Fatalf("对齐模式保留了未选中的分支：%q", out)
	}
}
