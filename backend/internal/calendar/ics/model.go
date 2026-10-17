// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package ics

import "strings"

// Object is the result of Parse: every iCalendar object (VCALENDAR) of the
// input in order, what was wrong with the input and whether a cap cut it.
type Object struct {
	// Calendars are the top-level components, always named VCALENDAR (a
	// synthetic one wraps components found outside any VCALENDAR).
	Calendars []*Component
	// Problems lists what the parser tolerated or dropped, at most
	// MaxProblems (+1 ProblemTooManyProblems).
	Problems []Problem
	// Truncated: a cap on depth, components or properties was reached;
	// something of the input is missing (never a partial component kept as a
	// child: a component cut by a cap is dropped whole, only top-level
	// VCALENDARs open at that moment are kept).
	Truncated bool
}

// Problem is one tolerated or dropped part of the input. Line is the
// 1-based physical line where the logical (unfolded) line started, 0 when
// the problem concerns the whole input.
type Problem struct {
	Line   int
	Code   string
	Detail string
}

// Component is BEGIN:Name … END:Name with its properties and child
// components, each in input order. Name is upper case.
type Component struct {
	Name       string
	Props      []Property
	Components []*Component
}

// Property is one content line. Name and parameter names are upper case;
// Value is the raw value as it was on the line (TEXT escapes intact: use
// Text to read it).
type Property struct {
	Name   string
	Params []Param
	Value  string
}

// Param is one parameter of a property; Values holds its comma-separated
// values with the quotes removed (always at least one, possibly "").
type Param struct {
	Name   string
	Values []string
}

// Calendar returns the first VCALENDAR, or nil.
func (o *Object) Calendar() *Component {
	if o == nil || len(o.Calendars) == 0 {
		return nil
	}
	return o.Calendars[0]
}

// HasProblems reports whether the parser noted anything or truncated.
func (o *Object) HasProblems() bool {
	return o != nil && (len(o.Problems) > 0 || o.Truncated)
}

// Property returns the first property named name (case-insensitive), or nil.
func (c *Component) Property(name string) *Property {
	if c == nil {
		return nil
	}
	for i := range c.Props {
		if strings.EqualFold(c.Props[i].Name, name) {
			return &c.Props[i]
		}
	}
	return nil
}

// Properties returns every property named name (case-insensitive).
func (c *Component) Properties(name string) []*Property {
	if c == nil {
		return nil
	}
	var out []*Property
	for i := range c.Props {
		if strings.EqualFold(c.Props[i].Name, name) {
			out = append(out, &c.Props[i])
		}
	}
	return out
}

// Children returns the child components named name (case-insensitive).
func (c *Component) Children(name string) []*Component {
	if c == nil {
		return nil
	}
	var out []*Component
	for _, ch := range c.Components {
		if strings.EqualFold(ch.Name, name) {
			out = append(out, ch)
		}
	}
	return out
}

// Add appends a property.
func (c *Component) Add(p Property) { c.Props = append(c.Props, p) }

// Param returns the first value of the parameter named name
// (case-insensitive), or "".
func (p *Property) Param(name string) string {
	if v := p.ParamValues(name); len(v) > 0 {
		return v[0]
	}
	return ""
}

// HasParam reports whether the property has the parameter.
func (p *Property) HasParam(name string) bool { return p.ParamValues(name) != nil }

// ParamValues returns the values of the first parameter named name, or nil.
func (p *Property) ParamValues(name string) []string {
	if p == nil {
		return nil
	}
	for _, pa := range p.Params {
		if strings.EqualFold(pa.Name, name) {
			if pa.Values == nil {
				return []string{}
			}
			return pa.Values
		}
	}
	return nil
}

// SetParam replaces the parameter (or appends it) with the given values.
func (p *Property) SetParam(name string, values ...string) {
	name = strings.ToUpper(name)
	if len(values) == 0 {
		values = []string{""}
	}
	for i := range p.Params {
		if p.Params[i].Name == name {
			p.Params[i].Values = values
			return
		}
	}
	p.Params = append(p.Params, Param{Name: name, Values: values})
}

// NewProperty builds a property with a raw value.
func NewProperty(name, value string, params ...Param) Property {
	return Property{Name: strings.ToUpper(name), Params: params, Value: value}
}

// NewText builds a TEXT property; text is escaped.
func NewText(name, text string, params ...Param) Property {
	return NewProperty(name, EscapeText(text), params...)
}

// val is the raw value, "" for a nil property: every value accessor may
// be called on the result of a lookup that found nothing.
func (p *Property) val() string {
	if p == nil {
		return ""
	}
	return p.Value
}
