// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
)

// TileWidth is the width of an attachment tile: three side by side fit a
// message pane of about 900 px, and the name keeps enough of itself at
// GNOME's text size (macOS has 216 pt at its smaller one).
const TileWidth = 280

// AttachmentTile is one attachment as a tile, the same under the headers
// of a message and in a compose window: a card of fixed width with the
// 32 px icon of the file's type, the name on the first line (its middle
// elided to the width, so the extension stays visible) over the size, and
// a trailing button (the arrow of the actions' menu, or the ✕ of a compose
// window). Main is the flat button that covers the icon and the text: its
// click is the tile's. Names are server data and are shown as plain text;
// the whole name is the tooltip unless the caller sets another. The
// look is CSS (box.attachment-tile in ui/internal/style).
type AttachmentTile struct {
	Root *gtk.Box
	Main *gtk.Button
	// Detail is the line of the size; a caller appends what follows it
	// (the server icon of a part on the mail server, a spinner).
	Detail *gtk.Box
}

// NewAttachmentTile builds a tile for a file of content type contentType
// (a gio content type, as gio.ContentTypeGuess gives it); size may be
// empty, accessory nil.
func NewAttachmentTile(contentType, name, size string, accessory gtk.Widgetter) *AttachmentTile {
	icon := gtk.NewImageFromGIcon(gio.ContentTypeGetIcon(contentType))
	icon.SetPixelSize(32)
	icon.SetVAlign(gtk.AlignCenter)

	label := gtk.NewLabel(name)
	label.SetUseMarkup(false)
	label.SetEllipsize(pango.EllipsizeMiddle)
	// Small natural width: the tile's width decides, the label takes what
	// is left of it.
	label.SetMaxWidthChars(1)
	label.SetHExpand(true)
	label.SetXAlign(0)
	label.AddCSSClass("tile-name")

	detail := gtk.NewBox(gtk.OrientationHorizontal, 4)
	if size != "" {
		sizeLabel := gtk.NewLabel(size)
		sizeLabel.SetUseMarkup(false)
		sizeLabel.SetXAlign(0)
		sizeLabel.AddCSSClass("tile-size")
		sizeLabel.AddCSSClass("dim-label")
		detail.Append(sizeLabel)
	}

	texts := gtk.NewBox(gtk.OrientationVertical, 1)
	texts.SetVAlign(gtk.AlignCenter)
	texts.SetHExpand(true)
	texts.Append(label)
	texts.Append(detail)

	content := gtk.NewBox(gtk.OrientationHorizontal, 8)
	content.Append(icon)
	content.Append(texts)

	main := gtk.NewButton()
	main.SetChild(content)
	main.SetHExpand(true)
	main.AddCSSClass("flat")
	main.AddCSSClass("tile-main")
	main.SetTooltipText(name)

	root := gtk.NewBox(gtk.OrientationHorizontal, 0)
	root.AddCSSClass("attachment-tile")
	root.SetSizeRequest(TileWidth, -1)
	// Main expands inside the tile only: set explicitly, so its hexpand
	// does not reach the wrap box, which would stretch the tiles of a line
	// over the leftover width.
	root.SetHExpand(false)
	root.SetHAlign(gtk.AlignStart)
	root.Append(main)
	if accessory != nil {
		acc := gtk.BaseWidget(accessory)
		acc.AddCSSClass("tile-accessory")
		acc.SetVAlign(gtk.AlignCenter)
		root.Append(accessory)
	}
	return &AttachmentTile{Root: root, Main: main, Detail: detail}
}

// FileContentType is the content type a tile's icon is looked up by: the
// declared type without parameters, or, when that says nothing
// (application/octet-stream), what the name suggests.
func FileContentType(filename, contentType string) string {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if ct == "" || ct == "application/octet-stream" {
		if _, guess := gio.ContentTypeGuess(filename, nil); guess != "" {
			ct = guess
		}
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	return ct
}

// SetInert makes the tile a label only: no click, no hover, no focus (an
// event's attachments, which have nothing to preview).
func (t *AttachmentTile) SetInert() {
	t.Main.SetCanTarget(false)
	t.Main.SetFocusable(false)
	t.Root.AddCSSClass("inert")
	t.Root.SetTooltipText(t.Main.TooltipText())
}

// OnSecondaryClick runs f on a right-click on the tile (the menu of the
// arrow, for a tile under the headers of a message).
func (t *AttachmentTile) OnSecondaryClick(f func()) {
	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_SECONDARY)
	click.ConnectPressed(func(n int, x, y float64) {
		click.SetState(gtk.EventSequenceClaimed)
		f()
	})
	t.Root.AddController(click)
}
