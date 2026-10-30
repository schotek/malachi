// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/contacts"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The contact form (W4) in the detail's place: the book for a new
// contact, the name parts, company and job title, the labelled rows of
// e-mail addresses, phones, postal addresses and websites (each with its
// label picker and a remove button, "Add …" under them), the birthday,
// the note and Delete Contact…; Done and Cancel are in the header bar.
// The widgets write into the contacts.Form as the user types; the form is
// built again only when its structure changes (a row added or removed,
// Reload after a conflict put the server's version under the user's
// changes) or another form opens. The validation's problems and a failed
// save's sentence stand above the fields, the fields concerned marked.

// contactForm is the form built into formBox.
type contactForm struct {
	f *contacts.Form
	// card is the card the form was built over (Rebase replaces it).
	card *api.ContactCard
	// rebuild: a row was added or removed.
	rebuild bool
	// focus is the field to focus after a rebuild ("" none).
	focus string

	problems *gtk.Box
	// fields are the widgets by problemKey, for the error marks.
	fields map[string]gtk.Widgetter
	// grab is the widget to focus once built.
	grab gtk.Widgetter
}

func problemKey(k contacts.FieldKey, i int) string { return fmt.Sprintf("%d:%d", k, i) }

// renderForm builds the form when needed and shows its problems.
func (cw *ContactsWindow) renderForm() {
	f := cw.contact.Form()
	if cw.form == nil || cw.form.f != f || cw.form.card != cw.contact.Card() || cw.form.rebuild {
		focus := ""
		if cw.form != nil && cw.form.f == f {
			focus = cw.form.focus
		}
		cw.buildForm(f, focus)
	}
	cw.detailKey = ""
	cw.formBox.SetSensitive(!cw.contact.Saving() && !cw.contact.Deleting())
	cw.renderProblems()
}

func (cw *ContactsWindow) renderProblems() {
	cf := cw.form
	for ch := cf.problems.FirstChild(); ch != nil; ch = cf.problems.FirstChild() {
		cf.problems.Remove(ch)
	}
	for _, w := range cf.fields {
		gtk.BaseWidget(w).RemoveCSSClass("error")
	}
	var lines []string
	if e := cw.contact.Error(); e != "" {
		lines = append(lines, e)
	}
	for _, p := range cw.contact.Problems() {
		lines = append(lines, p.Text)
		if w, ok := cf.fields[problemKey(p.Key, p.Index)]; ok {
			gtk.BaseWidget(w).AddCSSClass("error")
		}
	}
	for _, l := range lines {
		lb := gtk.NewLabel(l)
		lb.SetUseMarkup(false)
		lb.SetXAlign(0)
		lb.SetWrap(true)
		lb.AddCSSClass("error")
		cf.problems.Append(lb)
	}
	cf.problems.SetVisible(len(lines) > 0)
}

func (cw *ContactsWindow) buildForm(f *contacts.Form, focus string) {
	tr := i18n.Tr
	for ch := cw.formBox.FirstChild(); ch != nil; ch = cw.formBox.FirstChild() {
		cw.formBox.Remove(ch)
	}
	cf := &contactForm{f: f, card: cw.contact.Card(), problems: gtk.NewBox(gtk.OrientationVertical, 4),
		fields: map[string]gtk.Widgetter{}}
	cw.form = cf
	if f.IsNew {
		cw.detailPg.SetTitle(contacts.NewContact(tr))
	}
	cw.formBox.Append(cf.problems)

	if f.IsNew {
		choices := contacts.BookChoices(cw.ctl.Books(), cw.ctl.Accounts(), tr)
		if len(choices) > 0 {
			names := make([]string, len(choices))
			at := 0
			for i, c := range choices {
				names[i] = c.Text
				if c.Book == f.Book {
					at = i
				}
			}
			row := adw.NewComboRow()
			row.SetTitle(contacts.BookLabel(tr))
			row.SetModel(gtk.NewStringList(names))
			row.SetSelected(uint(at))
			row.NotifyProperty("selected", func() {
				if i := int(row.Selected()); i >= 0 && i < len(choices) {
					f.Book = choices[i].Book
				}
			})
			cw.formBox.Append(boxedList(row))
		}
	}

	text := func(k contacts.FieldKey, v *string) *adw.EntryRow {
		e := adw.NewEntryRow()
		e.SetTitle(contacts.Placeholder(k, tr))
		e.SetText(*v)
		e.ConnectChanged(func() { *v = e.Text() })
		cf.fields[problemKey(k, -1)] = e
		if cf.grab == nil {
			cf.grab = e
		}
		return e
	}
	cw.formBox.Append(boxedList(
		text(contacts.KeyPrefix, &f.Prefix),
		text(contacts.KeyGiven, &f.Given),
		text(contacts.KeyMiddle, &f.Middle),
		text(contacts.KeyFamily, &f.Family),
		text(contacts.KeySuffix, &f.Suffix),
		text(contacts.KeyNickname, &f.Nickname),
	))
	cw.formBox.Append(boxedList(
		text(contacts.KeyOrg, &f.Org),
		text(contacts.KeyTitle, &f.Title),
	))

	cw.formBox.Append(cw.labelledRows(cf, contacts.FieldEmail, contacts.KeyEmails, &f.Emails))
	cw.formBox.Append(cw.labelledRows(cf, contacts.FieldPhone, contacts.KeyPhones, &f.Phones))
	cw.formBox.Append(cw.addressRows(cf))
	cw.formBox.Append(cw.labelledRows(cf, contacts.FieldURL, contacts.KeyURLs, &f.URLs))
	cw.formBox.Append(cw.birthdayRow(cf))
	cw.formBox.Append(noteField(cf, &f.Note))

	if !f.IsNew && cw.contact.CanDelete() {
		del := gtk.NewButtonWithLabel(contacts.DeleteContact(tr))
		del.AddCSSClass("destructive-action")
		del.SetHAlign(gtk.AlignStart)
		del.ConnectClicked(func() { cw.contact.Delete() })
		cw.formBox.Append(del)
	}

	grab := cf.grab
	if w, ok := cf.fields[focus]; ok {
		grab = w
	}
	if grab != nil {
		gtk.BaseWidget(grab).GrabFocus()
	}
}

// boxedList is rows in a boxed list.
func boxedList(rows ...gtk.Widgetter) *gtk.ListBox {
	l := gtk.NewListBox()
	l.SetSelectionMode(gtk.SelectionNone)
	l.AddCSSClass("boxed-list")
	for _, r := range rows {
		l.Append(r)
	}
	return l
}

// labelPicker is a row's label choice.
func labelPicker(k contacts.FieldKind, label *api.ContactLabel) *gtk.DropDown {
	choices := contacts.LabelChoices(k, *label)
	names := make([]string, len(choices))
	at := 0
	for i, c := range choices {
		names[i] = contacts.LabelText(k, c, i18n.Tr)
		if c == *label {
			at = i
		}
	}
	dd := gtk.NewDropDownFromStrings(names)
	dd.SetSelected(uint(at))
	dd.SetVAlign(gtk.AlignCenter)
	dd.NotifyProperty("selected", func() {
		if i := int(dd.Selected()); i >= 0 && i < len(choices) {
			*label = choices[i]
		}
	})
	return dd
}

// removeButton is a row's remove button.
func removeButton(remove func()) *gtk.Button {
	b := gtk.NewButtonFromIconName("list-remove-symbolic")
	b.SetTooltipText(contacts.RemoveRowText(i18n.Tr))
	b.SetVAlign(gtk.AlignCenter)
	b.AddCSSClass("flat")
	b.ConnectClicked(remove)
	return b
}

// addButton is "Add Email" and the like.
func (cw *ContactsWindow) addButton(cf *contactForm, k contacts.FieldKind, key contacts.FieldKey) *gtk.Button {
	b := gtk.NewButtonWithLabel(contacts.AddRowText(k, i18n.Tr))
	b.AddCSSClass("flat")
	b.SetHAlign(gtk.AlignStart)
	b.SetSensitive(cf.f.CanAdd(k))
	b.ConnectClicked(func() {
		if i := cf.f.AddRow(k); i >= 0 {
			cf.focus = problemKey(key, i)
			cf.rebuild = true
			cw.schedule()
		}
	})
	return b
}

// labelledRows are the e-mail, phone or website rows with Add under them.
func (cw *ContactsWindow) labelledRows(cf *contactForm, k contacts.FieldKind, key contacts.FieldKey, rows *[]contacts.LabelledValue) *gtk.Box {
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	if len(*rows) > 0 {
		list := gtk.NewListBox()
		list.SetSelectionMode(gtk.SelectionNone)
		list.AddCSSClass("boxed-list")
		for i := range *rows {
			r := &(*rows)[i]
			e := gtk.NewEntry()
			e.SetText(r.Value)
			e.SetHExpand(true)
			e.SetPlaceholderText(contacts.Placeholder(key, i18n.Tr))
			switch k {
			case contacts.FieldEmail:
				e.SetInputPurpose(gtk.InputPurposeEmail)
			case contacts.FieldPhone:
				e.SetInputPurpose(gtk.InputPurposePhone)
			case contacts.FieldURL:
				e.SetInputPurpose(gtk.InputPurposeURL)
			}
			e.ConnectChanged(func() { r.Value = e.Text() })
			cf.fields[problemKey(key, i)] = e
			idx := i
			line := gtk.NewBox(gtk.OrientationHorizontal, 6)
			line.SetMarginTop(6)
			line.SetMarginBottom(6)
			line.SetMarginStart(6)
			line.SetMarginEnd(6)
			line.Append(labelPicker(k, &r.Label))
			line.Append(e)
			line.Append(removeButton(func() {
				cf.f.RemoveRow(k, idx)
				cf.rebuild = true
				cw.schedule()
			}))
			row := gtk.NewListBoxRow()
			row.SetActivatable(false)
			row.SetChild(line)
			list.Append(row)
		}
		box.Append(list)
	}
	box.Append(cw.addButton(cf, k, key))
	return box
}

// addressRows are the postal addresses: the label, the street, postal
// code and city, region and country.
func (cw *ContactsWindow) addressRows(cf *contactForm) *gtk.Box {
	tr := i18n.Tr
	f := cf.f
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	if len(f.Addresses) > 0 {
		list := gtk.NewListBox()
		list.SetSelectionMode(gtk.SelectionNone)
		list.AddCSSClass("boxed-list")
		for i := range f.Addresses {
			a := &f.Addresses[i]
			idx := i
			part := func(p contacts.AddressPart, v *string) *gtk.Entry {
				e := gtk.NewEntry()
				// A street of several lines shows on one; it is written
				// back only when the user edits it.
				e.SetText(strings.ReplaceAll(*v, "\n", ", "))
				e.SetPlaceholderText(contacts.AddressPlaceholder(p, tr))
				e.SetHExpand(true)
				e.ConnectChanged(func() { *v = e.Text() })
				return e
			}
			top := gtk.NewBox(gtk.OrientationHorizontal, 6)
			top.Append(labelPicker(contacts.FieldAddress, &a.Label))
			street := part(contacts.PartStreet, &a.Street)
			top.Append(street)
			top.Append(removeButton(func() {
				f.RemoveRow(contacts.FieldAddress, idx)
				cf.rebuild = true
				cw.schedule()
			}))
			city := gtk.NewBox(gtk.OrientationHorizontal, 6)
			pc := part(contacts.PartPostcode, &a.Postcode)
			pc.SetHExpand(false)
			pc.SetWidthChars(8)
			city.Append(pc)
			city.Append(part(contacts.PartCity, &a.City))
			rest := gtk.NewBox(gtk.OrientationHorizontal, 6)
			rest.Append(part(contacts.PartRegion, &a.Region))
			rest.Append(part(contacts.PartCountry, &a.Country))
			col := gtk.NewBox(gtk.OrientationVertical, 6)
			col.SetMarginTop(6)
			col.SetMarginBottom(6)
			col.SetMarginStart(6)
			col.SetMarginEnd(6)
			col.Append(top)
			col.Append(city)
			col.Append(rest)
			cf.fields[problemKey(contacts.KeyAddresses, i)] = street
			row := gtk.NewListBoxRow()
			row.SetActivatable(false)
			row.SetChild(col)
			list.Append(row)
		}
		box.Append(list)
	}
	box.Append(cw.addButton(cf, contacts.FieldAddress, contacts.KeyAddresses))
	return box
}

// birthdayRow is Add Birthday, or the day, the month and the optional
// year with a remove button.
func (cw *ContactsWindow) birthdayRow(cf *contactForm) gtk.Widgetter {
	tr := i18n.Tr
	f := cf.f
	if f.Birthday == nil {
		b := gtk.NewButtonWithLabel(contacts.AddBirthday(tr))
		b.AddCSSClass("flat")
		b.SetHAlign(gtk.AlignStart)
		b.ConnectClicked(func() {
			f.Birthday = &api.ContactBirthday{Month: 1, Day: 1}
			cf.focus = problemKey(contacts.KeyBirthday, -1)
			cf.rebuild = true
			cw.schedule()
		})
		return b
	}
	bd := f.Birthday
	label := gtk.NewLabel(contacts.Placeholder(contacts.KeyBirthday, tr))
	label.SetXAlign(0)
	label.SetHExpand(true)
	label.SetEllipsize(pango.EllipsizeEnd)
	day := gtk.NewSpinButtonWithRange(1, 31, 1)
	day.SetValue(float64(max(bd.Day, 1)))
	day.SetVAlign(gtk.AlignCenter)
	day.ConnectValueChanged(func() { bd.Day = day.ValueAsInt() })
	months := make([]string, 12)
	for m := range months {
		months[m] = widget.Strftime(time.Date(2000, time.Month(m+1), 1, 12, 0, 0, 0, time.UTC), "%B")
	}
	month := gtk.NewDropDownFromStrings(months)
	month.SetSelected(uint(min(max(bd.Month, 1), 12) - 1))
	month.SetVAlign(gtk.AlignCenter)
	month.NotifyProperty("selected", func() { bd.Month = int(month.Selected()) + 1 })
	year := gtk.NewEntry()
	year.SetMaxLength(4)
	year.SetWidthChars(5)
	year.SetInputPurpose(gtk.InputPurposeDigits)
	year.SetVAlign(gtk.AlignCenter)
	if bd.Year > 0 {
		year.SetText(strconv.Itoa(bd.Year))
	}
	year.ConnectChanged(func() {
		n, err := strconv.Atoi(strings.TrimSpace(year.Text()))
		if err != nil {
			n = 0
		}
		bd.Year = n
	})
	line := gtk.NewBox(gtk.OrientationHorizontal, 6)
	line.SetMarginTop(6)
	line.SetMarginBottom(6)
	line.SetMarginEnd(6)
	line.SetMarginStart(12)
	line.Append(label)
	line.Append(day)
	line.Append(month)
	line.Append(year)
	line.Append(removeButton(func() {
		f.Birthday = nil
		cf.rebuild = true
		cw.schedule()
	}))
	cf.fields[problemKey(contacts.KeyBirthday, -1)] = day
	row := gtk.NewListBoxRow()
	row.SetActivatable(false)
	row.SetChild(line)
	return boxedList(row)
}

// noteField is the note: a heading and a text view.
func noteField(cf *contactForm, note *string) *gtk.Box {
	heading := gtk.NewLabel(contacts.Placeholder(contacts.KeyNote, i18n.Tr))
	heading.SetXAlign(0)
	heading.AddCSSClass("heading")
	tv := gtk.NewTextView()
	tv.SetWrapMode(gtk.WrapWordChar)
	tv.SetAcceptsTab(false)
	tv.SetTopMargin(8)
	tv.SetBottomMargin(8)
	tv.SetLeftMargin(8)
	tv.SetRightMargin(8)
	tv.SetSizeRequest(-1, 90)
	buf := tv.Buffer()
	buf.SetText(*note)
	buf.ConnectChanged(func() {
		*note = buf.Text(buf.StartIter(), buf.EndIter(), false)
	})
	frame := gtk.NewFrame("")
	frame.SetChild(tv)
	cf.fields[problemKey(contacts.KeyNote, -1)] = frame
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.Append(heading)
	box.Append(frame)
	return box
}

// save is Done.
func (cw *ContactsWindow) save() {
	cw.contact.Save()
	cw.schedule()
}

// cancelEdit is Cancel: asks first when the form has changes.
func (cw *ContactsWindow) cancelEdit() {
	if !cw.contact.Dirty() {
		cw.contact.CancelEdit()
		return
	}
	cw.askDiscard(func() { cw.contact.CancelEdit() })
}
