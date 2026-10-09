package qkparser

// The parser-side half of the permission-carrying reference syntax (R2). The permission travels
// inside the type string as a suffix, so "&rw u int p = &x;" parses to base type "int&" plus the
// declaration's Perm/Scope; the type checker owns everything that interprets those permissions.

import "strings"

// ValidRefPerm reports whether s is a permission stack made of r/w/m.
func ValidRefPerm(s string) bool {
	switch s {
	case "r", "w", "m", "rw", "rm", "wm", "rwm":
		return true
	}
	return false
}

// ValidRefScope reports whether s is one of the four follow layers.
func ValidRefScope(s string) bool {
	switch s {
	case "u", "f", "a", "t":
		return true
	}
	return false
}

// RefBaseType turns a base type into the reference type declared by "&perm scope base", leaving an
// already-suffixed base untouched.
func RefBaseType(base string) string {
	if strings.HasSuffix(base, "&") {
		return base
	}
	return base + "&"
}
