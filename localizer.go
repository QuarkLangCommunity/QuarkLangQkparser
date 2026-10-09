package qkparser

import "fmt"

// Localizer renders a message template in the caller's language: the syntax layer keeps its templates
// as the catalog keys and hands them to whichever localizer is installed, so this package never
// depends on a message catalog of its own.
type Localizer interface {
	// T renders one template, substituting args when the template carries verbs.
	T(template string, args ...any) string
}

// verbatim is the localizer in force until SetLocalizer installs another one: it renders every
// template unchanged, which keeps the package free of dependencies.
type verbatim struct{}

// T renders a template without translating it, substituting args through fmt.Sprintf when there are any.
func (verbatim) T(template string, args ...any) string {
	if len(args) == 0 {
		return template
	}
	return fmt.Sprintf(template, args...)
}

// parserLocalizer is the localizer every message is rendered through.
var parserLocalizer Localizer = verbatim{}

// SetLocalizer installs the localizer used for messages produced by this package and returns the previous one.
func SetLocalizer(L Localizer) Localizer {
	prev := parserLocalizer
	parserLocalizer = L
	return prev
}

// msg renders a message template through the package localizer.
func msg(template string, args ...any) string { return parserLocalizer.T(template, args...) }
