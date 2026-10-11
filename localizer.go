package qkparser

//go:generate go run ./cmd/mkmsg

import "fmt"

// A message is rendered from the packed table this package owns (messages_gen.go): the message is an
// ID, the ID is an index into one contiguous blob, and rendering is a slice plus fmt. The syntax layer
// therefore carries its own English source and depends on no message catalog — but, unlike a catalog
// keyed by the text, its IDs are data another package can check (see Messages).
//
// Localization happens on the consumer's side, through the Localizer seam below: the consumer renders
// these IDs from its own catalog, which is what makes "every ID this module ships has a value in every
// language the consumer ships" a checkable claim instead of a convention.

// Localizer renders a message of this package in the caller's language. The consumer (the interpreter
// and the toolchain) implements it with its own catalog; this package ships only its English source.
type Localizer interface {
	// Msg renders message id with args substituted through the template's fmt verbs.
	Msg(id ID, args ...any) string
}

// sourceLocalizer is the localizer in force until an embedder installs another one: it renders this
// package's own English text, so a program that embeds the parser directly still reads correctly.
type sourceLocalizer struct{}

// Msg renders this package's canonical English text for the ID.
func (sourceLocalizer) Msg(id ID, args ...any) string { return RenderSource(id, args...) }

// parserLocalizer is the localizer every message of this package is rendered through.
var parserLocalizer Localizer = sourceLocalizer{}

// SetLocalizer installs the localizer used for messages produced by this package and returns the
// previous one, so a caller can restore it. It is the one seam an embedder needs.
func SetLocalizer(L Localizer) Localizer {
	prev := parserLocalizer
	parserLocalizer = L
	return prev
}

// msgID renders one message of this package in the installed localizer's language, substituting args
// when the template carries verbs. It is the only way this package produces a message: a call site
// names an ID, never a text, so rephrasing a message cannot change which message it is.
func msgID(id ID, args ...any) string { return parserLocalizer.Msg(id, args...) }

// RenderSource renders the canonical English text of an ID: the default used when no localizer is
// installed. It is exported because a consumer that fails to cover an ID should be able to fall back
// to the source text explicitly rather than to an empty string.
func RenderSource(id ID, args ...any) string {
	t := msgText(id)
	if len(args) == 0 {
		return t
	}
	return fmt.Sprintf(t, args...)
}
