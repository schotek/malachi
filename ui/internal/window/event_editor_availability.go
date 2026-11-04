// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"math"
	"time"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/diamondburned/gotk4/pkg/pangocairo"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The editor's availability rows (port of AvailabilityView.swift, Go
// availability.go through EventEditorController.AvailabilityRows): per
// person a name and a lane over the working day 8:00–18:00 of the start's
// day in the system zone, the busy spans grey (tentative lighter), the
// proposed time green; a person without free/busy reads "availability
// unknown". Hours every two along the top. Names are drawn as plain
// Pango text (never markup).

const (
	availabilityAxisStart  = 8
	availabilityAxisEnd    = 18
	availabilityRowHeight  = 20
	availabilityAxisHeight = 16
	availabilityNameWidth  = 120
)

// wireAvailability sets the axis's draw function.
func (ed *eventEditor) wireAvailability() {
	ed.axis.SetContentHeight(availabilityAxisHeight)
	ed.axis.SetDrawFunc(func(area *gtk.DrawingArea, cr *cairo.Context, width, height int) {
		ed.drawAvailability(area, cr, float64(width), float64(height))
	})
}

// drawAvailability draws the axis, the proposed time and the rows.
func (ed *eventEditor) drawAvailability(area *gtk.DrawingArea, cr *cairo.Context, width, height float64) {
	from, to := ed.ctl.AvailabilityDay()
	loc := ed.env.Loc
	if loc == nil {
		loc = time.Local
	}
	d := from.In(loc)
	axisFrom := time.Date(d.Year(), d.Month(), d.Day(), availabilityAxisStart, 0, 0, 0, loc)
	axisTo := time.Date(d.Year(), d.Month(), d.Day(), availabilityAxisEnd, 0, 0, 0, loc)
	barX, barW := float64(availabilityNameWidth), math.Max(0, width-availabilityNameWidth)
	x := func(t time.Time) float64 {
		total := axisTo.Sub(axisFrom).Seconds()
		if total <= 0 {
			return barX
		}
		f := math.Min(math.Max(t.Sub(axisFrom).Seconds()/total, 0), 1)
		return barX + f*barW
	}
	instant := func(f float64) time.Time {
		return from.Add(time.Duration(f * float64(to.Sub(from))))
	}
	fg := area.Color()
	set := func(alpha float32) {
		c := gdk.NewRGBA(fg.Red(), fg.Green(), fg.Blue(), fg.Alpha()*alpha)
		gdk.CairoSetSourceRGBA(cr, &c)
	}
	text := func(s string, px, py, maxW float64, alpha float32) {
		l := area.CreatePangoLayout(s)
		l.SetEllipsize(pango.EllipsizeEnd)
		l.SetWidth(int(maxW * pango.SCALE))
		fd := pango.NewFontDescription()
		fd.SetSize(int(8.5 * pango.SCALE))
		l.SetFontDescription(fd)
		set(alpha)
		cr.MoveTo(px, py)
		pangocairo.ShowLayout(cr, l)
	}

	// The hours, every two labelled, with hairlines.
	for h := availabilityAxisStart; h <= availabilityAxisEnd; h++ {
		t := time.Date(d.Year(), d.Month(), d.Day(), h, 0, 0, 0, loc)
		tx := x(t)
		set(0.12)
		cr.Rectangle(tx, availabilityAxisHeight, 1, height-availabilityAxisHeight)
		cr.Fill()
		if (h-availabilityAxisStart)%2 == 0 {
			lx := math.Min(math.Max(tx-14, barX), barX+barW-28)
			text(calendar.TimeText(t, ed.env), lx, 0, 40, 0.5)
		}
	}

	// The proposed time, behind the rows.
	in := ed.ctl.Input()
	sx, ex := x(in.Start), x(in.End)
	if ex > sx {
		green := gdk.NewRGBA(0.18, 0.76, 0.49, 0.22)
		gdk.CairoSetSourceRGBA(cr, &green)
		cr.Rectangle(sx, availabilityAxisHeight, ex-sx, height-availabilityAxisHeight)
		cr.Fill()
		green = gdk.NewRGBA(0.18, 0.76, 0.49, 1)
		gdk.CairoSetSourceRGBA(cr, &green)
		cr.Rectangle(sx, availabilityAxisHeight, 1.5, height-availabilityAxisHeight)
		cr.Rectangle(ex-1.5, availabilityAxisHeight, 1.5, height-availabilityAxisHeight)
		cr.Fill()
	}

	for i, row := range ed.rows {
		y := float64(availabilityAxisHeight + i*availabilityRowHeight)
		text(row.Label, 0, y+3, availabilityNameWidth-8, 1)
		set(0.08)
		editorRoundedRect(cr, barX, y+4, barW, availabilityRowHeight-8, 3)
		cr.Fill()
		if !row.Known {
			text(calendar.Unknown(i18n.Tr), barX+6, y+3, barW-12, 0.6)
			continue
		}
		for _, span := range row.Busy {
			bx := x(instant(span.From))
			bw := x(instant(span.To)) - bx
			if bw <= 0 {
				continue
			}
			alpha := float32(0.45)
			if span.ShowAs == api.ShowAsTentative {
				alpha = 0.25
			}
			set(alpha)
			editorRoundedRect(cr, bx, y+4, bw, availabilityRowHeight-8, 2)
			cr.Fill()
		}
	}
}

// editorRoundedRect adds a rounded rectangle to the path.
func editorRoundedRect(cr *cairo.Context, x, y, w, h, r float64) {
	r = math.Min(r, math.Min(w, h)/2)
	cr.NewPath()
	cr.Arc(x+w-r, y+r, r, -math.Pi/2, 0)
	cr.Arc(x+w-r, y+h-r, r, 0, math.Pi/2)
	cr.Arc(x+r, y+h-r, r, math.Pi/2, math.Pi)
	cr.Arc(x+r, y+r, r, math.Pi, 3*math.Pi/2)
	cr.ClosePath()
}
