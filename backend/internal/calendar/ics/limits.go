// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ics

import "errors"

// Limits of the parser. Input over a limit is never an error of the whole
// object: what exceeds it is dropped and recorded as a Problem, and the
// caps on components and properties set Object.Truncated.
const (
	// MaxBytes is the largest input Parse reads (ErrTooBig above). It is the
	// value of api.MaxICSBytes (docs/calendar.md §4.8); the package does not
	// import pkg/api, so the two are kept equal by hand.
	MaxBytes = 1 << 20
	// MaxLineBytes caps one unfolded content line; a longer line is dropped.
	MaxLineBytes = 64 << 10
	// MaxNameBytes caps property, parameter and component names.
	MaxNameBytes = 64
	// MaxParams caps the parameters of one property; a property with more is
	// dropped.
	MaxParams = 32
	// MaxDepth caps the nesting of components (VCALENDAR is depth 1).
	MaxDepth = 4
	// MaxComponents caps the components of one input, VCALENDARs included.
	MaxComponents = 500
	// MaxComponentProperties caps the properties of one component.
	MaxComponentProperties = 1000
	// MaxProperties caps the properties of one input.
	MaxProperties = 5000
	// MaxProblems caps Object.Problems; one more problem, ProblemTooManyProblems,
	// says that the list stops there.
	MaxProblems = 100
	// MaxDurationSeconds bounds a DURATION value (ten years of 3653 days).
	MaxDurationSeconds = 3653 * 24 * 60 * 60
)

// Errors. Accessors wrap ErrSyntax and ErrRange with the type of the value
// (never the value itself, which is hostile text); test with errors.Is.
var (
	// ErrTooBig: the input is longer than MaxBytes.
	ErrTooBig = errors.New("ics: object too big")
	// ErrMalformed: the parser or encoder hit a bug (a recovered panic).
	ErrMalformed = errors.New("ics: malformed object")
	// ErrSyntax: a value does not have the form of its type.
	ErrSyntax = errors.New("ics: invalid value")
	// ErrRange: a value has the form of its type but is out of bounds.
	ErrRange = errors.New("ics: value out of range")
	// ErrInvalid: Encode was given something the parser would not read back
	// (an invalid name, a quote or control character in a parameter value,
	// CR, LF or NUL in a value, invalid UTF-8).
	ErrInvalid = errors.New("ics: cannot encode")
)

// Problem codes (Problem.Code). They are stable identifiers; Detail is
// English technical text that never contains text of the input other than
// names, which are restricted to [A-Z0-9-].
const (
	ProblemEmpty             = "empty"             // no component at all
	ProblemInvalidUTF8       = "invalidUTF8"       // replaced by U+FFFD
	ProblemNUL               = "nulByte"           // NUL bytes removed
	ProblemLineTooLong       = "lineTooLong"       // line over MaxLineBytes, dropped
	ProblemBadLine           = "badLine"           // not a content line (name, colon), dropped
	ProblemBadParam          = "badParam"          // malformed parameter, property dropped
	ProblemTooManyParams     = "tooManyParams"     // over MaxParams, property dropped
	ProblemControlChar       = "controlChar"       // control character in a value, kept
	ProblemBadBegin          = "badBegin"          // BEGIN/END without a valid name, ignored
	ProblemStrayEnd          = "strayEnd"          // END without an open component, ignored
	ProblemStrayProperty     = "strayProperty"     // property outside any component, dropped
	ProblemMismatchedEnd     = "mismatchedEnd"     // component closed by the END of an outer one
	ProblemMissingEnd        = "missingEnd"        // component closed by the end of input or a new VCALENDAR
	ProblemNoVCalendar       = "noVCalendar"       // top-level components wrapped in a synthetic VCALENDAR
	ProblemMultipleCalendars = "multipleCalendars" // more than one VCALENDAR, all kept
	ProblemTooDeep           = "tooDeep"           // component over MaxDepth, dropped whole (Truncated)
	ProblemTooManyComponents = "tooManyComponents" // MaxComponents reached, the rest not read (Truncated)
	ProblemTooManyProperties = "tooManyProperties" // a component over MaxComponentProperties dropped whole, or MaxProperties reached (Truncated)
	ProblemTooManyProblems   = "tooManyProblems"   // MaxProblems reached, further problems not listed
)
