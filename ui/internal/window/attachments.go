// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/core/gerror"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The attachment chips under the headers of a message (message_attachments
// in both .blp files): one per attachment with its icon, name and size. A
// click opens the attachment, the arrow offers Open and Save As…, and Save
// All appears with two or more. Names and types are server data and are
// shown as plain text (CLAUDE.md rule 3); programs and scripts are never
// opened directly (docs/security.md §4); the content comes through
// message.part, so a part over api.MaxAttachmentDataBytes is out of reach.

// partTimeout bounds one message.part call: the part may be 16 MiB of
// base64 on the socket, far more than rpcTimeout allows for.
const partTimeout = 60 * time.Second

// openMaxAge is how long a file written for opening is kept before the
// next open sweeps it: the viewer may still be reading it lazily.
const openMaxAge = time.Hour

// renderAttachments rebuilds the chips for what lm holds: nothing until
// message.get answered, otherwise every attachment except the pictures the
// HTML body on display already shows.
func (v *messageView) renderAttachments(s api.MessageSummary, lm *loadedMessage) {
	for _, c := range v.chips {
		v.attachments.Remove(c)
	}
	v.chips = nil
	if lm == nil || lm.msg == nil {
		v.attachments.SetVisible(false)
		return
	}
	atts := chipAttachments(lm.msg.Attachments, lm.body)
	if len(atts) == 0 {
		v.attachments.SetVisible(false)
		return
	}
	allOK := true
	for _, a := range atts {
		ok, why := partAvailable(a, lm.body)
		allOK = allOK && ok
		v.addChip(v.buildChip(s.AccountID, s.ID, a, ok, why))
	}
	if len(atts) >= 2 && allOK {
		v.addChip(v.buildSaveAll(s.AccountID, s.ID, atts))
	}
	v.attachments.SetVisible(true)
}

// addChip appends a widget to the chip box and remembers it for removal.
func (v *messageView) addChip(c gtk.Widgetter) {
	v.attachments.Append(c)
	v.chips = append(v.chips, c)
}

// say shows a toast in the window that owns the view, if it wired one.
func (v *messageView) say(text string) {
	if v.toast != nil {
		v.toast(text)
	}
}

// buildChip is one attachment: a button (icon, name, size) that opens it,
// linked to an arrow with the Open / Save As… menu. The actions live in a
// group on the chip itself and close over the attachment, so a chip never
// acts on a message other than the one it was built for. An unavailable
// part (ok false) leaves the chip insensitive with why as its tooltip; an
// executable keeps Open disabled and saves on click instead.
func (v *messageView) buildChip(acc api.AccountID, id api.MessageID, a api.Attachment, ok bool, why string) gtk.Widgetter {
	name := chipName(a)
	exe := executableAttachment(a.Filename, a.ContentType)

	icon := gtk.NewImageFromGIcon(gio.ContentTypeGetSymbolicIcon(chipIconType(a)))
	label := gtk.NewLabel(name)
	label.SetUseMarkup(false)
	label.SetEllipsize(pango.EllipsizeMiddle) // keep the extension visible
	label.SetMaxWidthChars(28)
	inner := gtk.NewBox(gtk.OrientationHorizontal, 6)
	inner.Append(icon)
	inner.Append(label)
	if a.Size > 0 {
		size := gtk.NewLabel(widget.FormatSize(a.Size))
		size.SetUseMarkup(false)
		size.AddCSSClass("caption")
		size.AddCSSClass("dim-label")
		inner.Append(size)
	}
	button := gtk.NewButton()
	button.SetChild(inner)
	arrow := gtk.NewMenuButton()
	arrow.SetIconName("pan-down-symbolic")
	arrow.SetMenuModel(chipMenu())
	arrow.SetTooltipText(i18n.T("More Actions"))

	box := gtk.NewBox(gtk.OrientationHorizontal, 0)
	box.AddCSSClass("linked")
	box.AddCSSClass("attachment-chip")
	box.Append(button)
	box.Append(arrow)

	open := func() { v.openAttachment(acc, id, a) }
	save := func() { v.saveAttachment(acc, id, a) }
	g := gio.NewSimpleActionGroup()
	openAction := gio.NewSimpleAction("open", nil)
	openAction.SetEnabled(!exe)
	openAction.ConnectActivate(func(*glib.Variant) { open() })
	saveAction := gio.NewSimpleAction("save", nil)
	saveAction.ConnectActivate(func(*glib.Variant) { save() })
	g.AddAction(openAction)
	g.AddAction(saveAction)
	box.InsertActionGroup("att", g)

	switch {
	case !ok:
		box.SetSensitive(false)
		button.SetTooltipText(why)
		arrow.SetTooltipText(why)
	case exe:
		button.SetTooltipText(i18n.T("Programs and scripts are not opened directly; save the file and decide yourself."))
		button.ConnectClicked(save)
	default:
		button.SetTooltipText(name)
		button.ConnectClicked(open)
	}
	return box
}

// chipMenu is the arrow's menu; the actions resolve on the chip.
func chipMenu() *gio.Menu {
	m := gio.NewMenu()
	m.Append(i18n.T("_Open"), "att.open")
	m.Append(i18n.T("Save _As…"), "att.save")
	return m
}

// buildSaveAll is the button after the chips that saves all of them into
// one folder.
func (v *messageView) buildSaveAll(acc api.AccountID, id api.MessageID, atts []api.Attachment) gtk.Widgetter {
	button := gtk.NewButton()
	button.AddCSSClass("flat")
	content := gtk.NewBox(gtk.OrientationHorizontal, 6)
	content.Append(gtk.NewImageFromIconName("document-save-symbolic"))
	label := gtk.NewLabelWithMnemonic(i18n.T("Save _All"))
	content.Append(label)
	button.SetChild(content)
	label.SetMnemonicWidget(button)
	button.ConnectClicked(func() { v.saveAllAttachments(acc, id, atts, button) })
	return button
}

// showsHTML reports whether renderBody puts b in the HTML view.
func showsHTML(b *api.MessageBodyResult) bool {
	return b != nil && b.BodyState == api.BodyFetched && b.HTML != ""
}

// chipAttachments is what gets a chip: every attachment, minus the parts
// the HTML on display shows inline (the cid: references the sanitiser kept,
// b.InlineParts). A text-only message, withheld HTML or an unreferenced
// Content-ID leaves the part listed like any other file.
func chipAttachments(atts []api.Attachment, b *api.MessageBodyResult) []api.Attachment {
	html := showsHTML(b)
	out := make([]api.Attachment, 0, len(atts))
	for _, a := range atts {
		if html && a.ContentID != "" && a.PartID != "" && b.InlineParts[a.ContentID] == a.PartID {
			continue
		}
		out = append(out, a)
	}
	return out
}

// partAvailable reports whether message.part can deliver a, and if not
// why, as the chip's tooltip (empty while the body is still on its way).
// The daemon reads parts from the stored raw message only, and never
// beyond api.MaxAttachmentDataBytes; the size is exact once the body is
// fetched (before that it is the transfer size from BODYSTRUCTURE).
func partAvailable(a api.Attachment, b *api.MessageBodyResult) (bool, string) {
	if b == nil {
		return false, ""
	}
	switch b.BodyState {
	case api.BodyFetched:
	case api.BodyPending:
		return false, i18n.T("This message has not been downloaded yet.")
	case api.BodyTooBig:
		return false, i18n.T("This message is too large to download.")
	case api.BodyFailed:
		return false, i18n.T("This message could not be read.")
	default:
		return false, ""
	}
	if a.Size > api.MaxAttachmentDataBytes {
		// TRANSLATORS: %s is a size such as "16.0 MiB".
		return false, fmt.Sprintf(i18n.T("Attachments over %s cannot be opened or saved yet."), widget.FormatSize(api.MaxAttachmentDataBytes))
	}
	return true, ""
}

// executableExtensions and executableTypes are what the UI refuses to hand
// to the default application: anything the desktop might run rather than
// display. Judged by the last extension and the claimed type; either is
// enough. gio.ContentTypeCanBeExecutable is not used because it says yes
// to text/plain.
var executableExtensions = map[string]bool{
	"exe": true, "com": true, "bat": true, "cmd": true, "msi": true, "scr": true,
	"pif": true, "ps1": true, "vbs": true, "vbe": true, "js": true, "jse": true,
	"wsf": true, "wsh": true, "hta": true, "jar": true, "sh": true, "bash": true,
	"zsh": true, "run": true, "bin": true, "appimage": true, "desktop": true,
	"py": true, "pl": true, "rb": true, "php": true, "lnk": true, "reg": true,
	"dll": true, "so": true,
}

var executableTypes = map[string]bool{
	"application/x-executable":                     true,
	"application/x-sharedlib":                      true,
	"application/x-shellscript":                    true,
	"application/x-desktop":                        true,
	"application/x-ms-dos-executable":              true,
	"application/x-msdownload":                     true,
	"application/x-msi":                            true,
	"application/vnd.microsoft.portable-executable": true,
	"application/x-elf":                            true,
	"application/x-pie-executable":                 true,
	"application/java-archive":                     true,
	"application/x-java-archive":                   true,
	"application/vnd.appimage":                     true,
	"application/x-iso9660-appimage":               true,
	"application/x-bat":                            true,
	"application/x-msdos-program":                  true,
	"application/x-perl":                           true,
	"application/javascript":                       true,
	"application/x-ms-shortcut":                    true,
	"application/hta":                              true,
	"text/x-shellscript":                           true,
	"text/x-python":                                true,
	"text/x-perl":                                  true,
	"text/javascript":                              true,
	"text/x-msdos-batch":                           true,
}

// executableAttachment reports whether an attachment must not be opened
// directly (docs/security.md §4).
func executableAttachment(filename, contentType string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(strings.TrimSpace(filename)), "."))
	if ext != "" && executableExtensions[ext] {
		return true
	}
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return executableTypes[ct]
}

// chipName is the chip's label: the sanitised file name, or a placeholder
// for a part without one (the daemon names those, so this is a fallback).
func chipName(a api.Attachment) string {
	if n := strings.TrimSpace(a.Filename); n != "" {
		return n
	}
	return i18n.T("Attachment")
}

// chipIconType is the content type the icon is chosen by: the claimed one,
// or a guess from the file name when the sender said nothing useful.
func chipIconType(a api.Attachment) string {
	ct := strings.ToLower(strings.TrimSpace(a.ContentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if ct == "" || ct == "application/octet-stream" {
		if _, guess := gio.ContentTypeGuess(a.Filename, nil); guess != "" {
			ct = guess
		}
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	return ct
}

// fileName is the name a fetched part is written under: what message.part
// reported, else the listed name, else a constant; never a path (the
// daemon sanitises, this is defence in depth).
func fileName(res *api.MessagePartResult, a api.Attachment) string {
	for _, n := range []string{res.Filename, a.Filename} {
		n = filepath.Base(strings.TrimSpace(n))
		if n != "" && n != "." && n != string(filepath.Separator) {
			return n
		}
	}
	return "attachment"
}

// uniqueName is name, or name with " (2)", " (3)", … before the extension
// until taken says no. It gives up after 1000 tries and returns name.
func uniqueName(name string, taken func(string) bool) string {
	if !taken(name) {
		return name
	}
	base, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		base, ext = name[:i], name[i:]
	}
	for n := 2; n < 1000; n++ {
		c := fmt.Sprintf("%s (%d)%s", base, n, ext)
		if !taken(c) {
			return c
		}
	}
	return name
}

// saveAllSummary is the toast after Save All.
func saveAllSummary(saved, failed int) string {
	if failed == 0 {
		// TRANSLATORS: %d is the number of files written.
		return fmt.Sprintf(i18n.N("Saved %d attachment", "Saved %d attachments", saved), saved)
	}
	// TRANSLATORS: the first %d is how many failed, the second how many there were.
	return fmt.Sprintf(i18n.T("%d of %d attachments could not be saved"), failed, saved+failed)
}

// fetchAttachment is message.part for one attachment. It runs off the main
// loop.
func (w *Window) fetchAttachment(ctx context.Context, acc api.AccountID, id api.MessageID, part string) (*api.MessagePartResult, error) {
	var res api.MessagePartResult
	if err := w.client.Call(ctx, api.MethodMessagePart, api.MessagePartParams{AccountID: acc, MessageID: id, PartID: part}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// openAttachment writes the part to a private file and hands it to the
// default application (the OpenURI portal under Flatpak).
func (v *messageView) openAttachment(acc api.AccountID, id api.MessageID, a api.Attachment) {
	parent, w := v.parent, v.win
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), partTimeout)
		defer cancel()
		res, err := w.fetchAttachment(ctx, acc, id, a.PartID)
		if err != nil {
			w.log.Warn("message.part", "part", a.PartID, "err", err)
			glib.IdleAdd(func() { v.say(widget.RPCErrorText(i18n.T("Opening the attachment"), err)) })
			return
		}
		path, err := writeOpenFile(fileName(res, a), res.Data)
		if err != nil {
			w.log.Warn("writing an attachment for opening", "err", err)
			glib.IdleAdd(func() { v.say(i18n.T("The attachment could not be opened")) })
			return
		}
		glib.IdleAdd(func() {
			l := gtk.NewFileLauncher(gio.NewFileForPath(path))
			l.Launch(context.Background(), parent, func(r gio.AsyncResulter) {
				if err := l.LaunchFinish(r); err != nil && !dialogDismissed(err) {
					w.log.Warn("opening an attachment", "err", err)
					v.say(i18n.T("The attachment could not be opened"))
				}
			})
		})
	}()
}

// saveAttachment asks where to put the part (the FileChooser portal under
// Flatpak), then fetches and writes it. The dialog already confirmed an
// overwrite, so the write replaces; only a failure gets a toast.
func (v *messageView) saveAttachment(acc api.AccountID, id api.MessageID, a api.Attachment) {
	w := v.win
	dlg := gtk.NewFileDialog()
	dlg.SetTitle(i18n.T("Save Attachment"))
	dlg.SetInitialName(fileName(&api.MessagePartResult{}, a))
	dlg.Save(context.Background(), v.parent, func(r gio.AsyncResulter) {
		f, err := dlg.SaveFinish(r)
		if err != nil {
			return // dismissed
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), partTimeout)
			defer cancel()
			res, err := w.fetchAttachment(ctx, acc, id, a.PartID)
			if err == nil {
				err = writeGFile(ctx, f, res.Data, true)
			}
			if err != nil {
				w.log.Warn("saving an attachment", "part", a.PartID, "err", err)
				glib.IdleAdd(func() { v.say(widget.RPCErrorText(i18n.T("Saving the attachment"), err)) })
			}
		}()
	})
}

// saveAllAttachments asks for a folder and writes every attachment into
// it, one message.part at a time, never overwriting: a name that exists
// gets " (2)" and so on. One toast sums it up; button is disabled while it
// runs.
func (v *messageView) saveAllAttachments(acc api.AccountID, id api.MessageID, atts []api.Attachment, button *gtk.Button) {
	w := v.win
	dlg := gtk.NewFileDialog()
	dlg.SetTitle(i18n.T("Save Attachments"))
	dlg.SelectFolder(context.Background(), v.parent, func(r gio.AsyncResulter) {
		folder, err := dlg.SelectFolderFinish(r)
		if err != nil {
			return // dismissed
		}
		button.SetSensitive(false)
		go func() {
			saved, failed := 0, 0
			for _, a := range atts {
				ctx, cancel := context.WithTimeout(context.Background(), partTimeout)
				err := w.saveInto(ctx, folder, acc, id, a)
				cancel()
				if err != nil {
					w.log.Warn("saving an attachment", "part", a.PartID, "err", err)
					failed++
				} else {
					saved++
				}
			}
			glib.IdleAdd(func() {
				button.SetSensitive(true)
				v.say(saveAllSummary(saved, failed))
			})
		}()
	})
}

// saveInto fetches a and creates it in folder under a name that is not
// taken yet. Off the main loop.
func (w *Window) saveInto(ctx context.Context, folder *gio.File, acc api.AccountID, id api.MessageID, a api.Attachment) error {
	res, err := w.fetchAttachment(ctx, acc, id, a.PartID)
	if err != nil {
		return err
	}
	name := fileName(res, a)
	// Create fails on an existing file; a name that appeared between the
	// check and the create is tried again, a few times.
	for try := 0; try < 8; try++ {
		name = uniqueName(name, func(n string) bool { return folder.Child(n).QueryExists(ctx) })
		err = writeGFile(ctx, folder.Child(name), res.Data, false)
		if !gioErrorIs(err, gio.IOErrorExists) {
			return err
		}
	}
	return err
}

// writeGFile writes data to f through GIO (a portal path need not be a
// local one): replacing the file, or creating it and failing when it
// exists.
func writeGFile(ctx context.Context, f *gio.File, data []byte, replace bool) error {
	var out *gio.FileOutputStream
	var err error
	if replace {
		out, err = f.Replace(ctx, "", false, gio.FileCreateReplaceDestination)
	} else {
		out, err = f.Create(ctx, gio.FileCreateNone)
	}
	if err != nil {
		return err
	}
	if _, err := out.WriteAll(ctx, data); err != nil {
		_ = out.Close(ctx)
		return err
	}
	return out.Close(ctx)
}

// gioErrorIs reports whether err is the GIO error code.
func gioErrorIs(err error, code gio.IOErrorEnum) bool {
	var g *gerror.GError
	return errors.As(err, &g) && g.Quark() == gio.IOErrorQuark() && g.ErrorCode() == int(code)
}

// dialogDismissed reports whether err only says the user closed or
// cancelled a dialog (the application chooser of a launch, for one).
func dialogDismissed(err error) bool {
	var g *gerror.GError
	if !errors.As(err, &g) || g.Quark() != gtk.DialogErrorQuark() {
		return false
	}
	return g.ErrorCode() == int(gtk.DialogErrorDismissed) || g.ErrorCode() == int(gtk.DialogErrorCancelled)
}

// openDir is where attachments being opened are written
// (docs/security.md §8): under the runtime dir, or the cache dir without
// one.
func openDir() string {
	base := glib.GetUserRuntimeDir()
	if base == "" {
		base = glib.GetUserCacheDir()
	}
	return filepath.Join(base, "malachi", "open")
}

// writeOpenFile writes data as name into a fresh private directory under
// openDir and returns its path. Entries older than openMaxAge go first.
func writeOpenFile(name string, data []byte) (string, error) {
	dir := openDir()
	sweepOpenDir(dir, openMaxAge)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sub, err := os.MkdirTemp(dir, "")
	if err != nil {
		return "", err
	}
	path := filepath.Join(sub, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.RemoveAll(sub)
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.RemoveAll(sub)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.RemoveAll(sub)
		return "", err
	}
	return path, nil
}

// sweepOpenDir removes the entries of dir older than maxAge.
func sweepOpenDir(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// SweepOpenedAttachments removes every file written for opening; main.go
// calls it when the application starts and when it exits.
func SweepOpenedAttachments() {
	_ = os.RemoveAll(openDir())
}
