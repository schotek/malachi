// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"slices"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/mcpsetup"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The Assistant menu (ui/internal/assistant) hands the selected mail to
// Claude Code on this computer: a claude-cli:// link opens it with a
// prepared question, prefilled and unsent, that carries only the API's
// opaque ids. Claude reads the mail
// itself through the malachi-mcp bridge that Settings → AI registers, so
// the Assistant exists only while that registration does (assistant.Shown).
// The menu is the ✦ button of the message pane and of a message window,
// an attachment's menu has "Ask the Assistant…", and Settings → AI has the
// Assistant group. The macOS client leads (MalachiCore
// AssistantController, MalachiMail Assistant/); this is its port.
//
// This client opens Claude Code only, in the terminal its claude-cli://
// handler picks ($TERMINAL, then x-terminal-emulator, then the common
// emulators). Claude Desktop for Linux is a preview the project does not
// support: the menu and the settings list it, insensitive. The third target
// of the reference, In App (the panel that runs Claude Code itself), is not
// in this client yet. Whatever is stored reads as Claude Code here
// (gtkTarget); the gschema keeps the reference's default.

// assistantIcon is the ✦ button's icon, shipped in ui/data/icons (the
// hicolor theme when installed, MALACHI_ICON_DIR in a source tree).
const assistantIcon = "malachi-assistant-symbolic"

// assistantTargets are the targets the menu and the settings list, in
// their order (assistant.Targets without App); only the supported ones can
// be chosen.
var assistantTargets = []assistant.Target{assistant.Desktop, assistant.Code}

// supportedTarget says whether this client opens target t: Claude Code
// only.
func supportedTarget(t assistant.Target) bool {
	return t == assistant.Code
}

// gtkTarget is the stored target as this client uses it: one it does not
// support reads as Claude Code.
func gtkTarget(t assistant.Target) assistant.Target {
	if supportedTarget(t) {
		return t
	}
	return assistant.Code
}

// catalog is the assistant.Translator over the application's catalog.
type catalog struct{}

func (catalog) T(msgid string) string                   { return i18n.T(msgid) }
func (catalog) N(singular, plural string, n int) string { return i18n.N(singular, plural, n) }

// tr translates the texts of ui/internal/assistant.
var tr assistant.Translator = catalog{}

// Assistant is what the Assistant menus know about the two Claude apps,
// once for the whole application: whether an app handles each target's
// links (GIO's default handler of the URL scheme) and whether the bridge
// is registered in each client (the last malachi-mcp status). The menus
// never wait for it: Refresh looks the handlers up at once and asks the
// bridge in the background (skipped while it asks), and whoever listens
// with OnChange hears what changed. A status not known yet (not asked, no
// bridge, the bridge failed) counts as not registered; a failed status
// keeps the last known one. The Preferences dialog hands over every status
// its "Register with Claude" gets (Apply), so a change counts at once.
//
// Main loop only.
type Assistant struct {
	settings *settings.Store
	log      *slog.Logger
	// bridge is malachi-mcp; "" without one (not found, or the Flatpak
	// build, where the Claude apps can neither see nor start it).
	bridge string

	status   *mcpsetup.Status
	handlers map[assistant.Target]bool
	querying bool

	observers map[int]func()
	nextID    int
}

// NewAssistant looks for the bridge and follows the assistant keys of s;
// Refresh asks for the rest.
func NewAssistant(s *settings.Store, log *slog.Logger) *Assistant {
	a := &Assistant{
		settings:  s,
		log:       log.With("component", "assistant"),
		handlers:  make(map[assistant.Target]bool),
		observers: make(map[int]func()),
	}
	if os.Getenv("FLATPAK_ID") == "" {
		if p, err := mcpsetup.Locate(); err == nil {
			a.bridge = p
		} else {
			a.log.Debug("no MCP bridge; the Assistant stays off", "err", err)
		}
	}
	for _, key := range []string{settings.KeyAssistantMenu, settings.KeyAssistantTarget} {
		s.OnChanged(key, a.notify)
	}
	return a
}

// AddActions registers the application actions of the Assistant menus:
// app.assistant-target (the "Open In" choice, the assistant-target key),
// app.assistant-setup ("Set Up the Assistant…", which calls setUp), and
// two that are never enabled: app.assistant-unsupported, the choice of a
// target this client does not support (a radio item, unchecked and
// insensitive), and app.assistant-problem, the menu item that says why the
// chosen target cannot run the message actions.
func (a *Assistant) AddActions(app *adw.Application, setUp func()) {
	target := gio.NewSimpleActionStateful("assistant-target", glib.NewVariantType("s"),
		glib.NewVariantString(string(gtkTarget(a.settings.AssistantTarget()))))
	target.ConnectActivate(func(v *glib.Variant) {
		if v != nil {
			a.settings.SetAssistantTarget(assistant.Target(v.String()))
		}
	})
	a.settings.OnChanged(settings.KeyAssistantTarget, func() {
		target.SetState(glib.NewVariantString(string(gtkTarget(a.settings.AssistantTarget()))))
	})
	app.AddAction(target)

	setup := gio.NewSimpleAction("assistant-setup", nil)
	setup.ConnectActivate(func(*glib.Variant) { setUp() })
	app.AddAction(setup)

	unsupported := gio.NewSimpleActionStateful("assistant-unsupported", glib.NewVariantType("s"), glib.NewVariantString(""))
	unsupported.SetEnabled(false)
	app.AddAction(unsupported)

	problem := gio.NewSimpleAction("assistant-problem", nil)
	problem.SetEnabled(false)
	app.AddAction(problem)
}

// Refresh looks the handlers up now and asks the bridge for its status in
// the background.
func (a *Assistant) Refresh() {
	a.refreshHandlers()
	a.query()
}

// refreshHandlers looks up whether an app handles the links of each
// supported target.
func (a *Assistant) refreshHandlers() {
	found := make(map[assistant.Target]bool, len(assistantTargets))
	for _, t := range assistantTargets {
		if supportedTarget(t) {
			found[t] = gio.AppInfoGetDefaultForURIScheme(t.Scheme()) != nil
		}
	}
	if maps.Equal(found, a.handlers) {
		return
	}
	a.handlers = found
	a.notify()
}

// query asks the bridge for its status, unless one is asked already or
// there is no bridge.
func (a *Assistant) query() {
	if a.bridge == "" || a.querying {
		return
	}
	a.querying = true
	bridge := a.bridge
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), mcpsetup.Timeout)
		defer cancel()
		st, err := mcpsetup.Query(ctx, bridge)
		glib.IdleAdd(func() {
			a.querying = false
			if err != nil {
				a.log.Debug("malachi-mcp status", "err", err)
				return
			}
			a.Apply(st)
		})
	}()
}

// Apply takes a status the bridge reported elsewhere (Settings → AI after
// status, install or uninstall).
func (a *Assistant) Apply(st mcpsetup.Status) {
	if a.status != nil && sameStatus(*a.status, st) {
		return
	}
	a.status = &st
	a.notify()
}

// sameStatus says whether two reports say the same.
func sameStatus(x, y mcpsetup.Status) bool {
	return x.Command == y.Command && slices.Equal(x.Clients, y.Clients)
}

// hasBridge says whether the Assistant can ever be shown: there is a
// bridge to register.
func (a *Assistant) hasBridge() bool { return a.bridge != "" }

// known says whether any status answered.
func (a *Assistant) known() bool { return a.status != nil }

// registered says whether the bridge is registered in at least one client,
// as last reported; false while no status is known.
func (a *Assistant) registered() bool {
	return a.status != nil && a.status.Registered()
}

// shown says whether the Assistant appears at all (assistant.Shown).
func (a *Assistant) shown() bool {
	return assistant.Shown(a.settings.AssistantMenu(), a.registered())
}

// target is the chosen target (gtkTarget).
func (a *Assistant) target() assistant.Target {
	return gtkTarget(a.settings.AssistantTarget())
}

// availability is what is known about target t; a target this client
// does not support has nothing.
func (a *Assistant) availability(t assistant.Target) assistant.Availability {
	if !supportedTarget(t) {
		return assistant.Availability{}
	}
	return targetAvailability(a.status, a.handlers, t)
}

// targetAvailability is the availability of target t for a status (nil:
// not known) and the handlers looked up: an app handles its links, the
// bridge is registered in its client (assistant.Target.ClientID).
func targetAvailability(st *mcpsetup.Status, handlers map[assistant.Target]bool, t assistant.Target) assistant.Availability {
	av := assistant.Availability{Handler: handlers[t]}
	if st != nil {
		for _, c := range st.Clients {
			if c.ID == t.ClientID() {
				av.Registered = c.Registered
			}
		}
	}
	return av
}

// pick is the chosen target and whether it can run an action
// (assistant.Pick, never another target instead): the message actions
// and Summarize Unread need the bridge, handing over a file does not.
func (a *Assistant) pick(needsBridge bool) (assistant.Target, bool) {
	return assistant.Pick(a.target(), a.availability(assistant.Desktop), a.availability(assistant.Code),
		assistant.Availability{}, needsBridge)
}

// problem says why target t cannot run the message actions; "" when it
// can.
func (a *Assistant) problem(t assistant.Target) string {
	return assistant.Problem(tr, t, a.availability(t))
}

// OnChange calls f after the handlers, the status or an assistant key
// changed; remove stops it.
func (a *Assistant) OnChange(f func()) (remove func()) {
	id := a.nextID
	a.nextID++
	a.observers[id] = f
	return func() { delete(a.observers, id) }
}

// notify calls the observers in the order they came.
func (a *Assistant) notify() {
	// Copy first: a handler may remove itself.
	fs := make([]func(), 0, len(a.observers))
	for _, id := range slices.Sorted(maps.Keys(a.observers)) {
		fs = append(fs, a.observers[id])
	}
	for _, f := range fs {
		f()
	}
}

// assistantMenu is the model of an Assistant menu: the message actions on
// what the window shows (prefix.assistant with the action's name), with
// unread Summarize Unread in This Folder (win.assistant-unread), "Open In"
// with the targets as a choice (app.assistant-target; one this client does
// not support on app.assistant-unsupported, insensitive), and, while the
// chosen target cannot run the message actions, a disabled item that says
// why above "Set Up the Assistant…". update brings that last section up to
// date; the button calls it as its menu opens.
func (a *Assistant) assistantMenu(prefix string, unread bool) (menu *gio.Menu, update func()) {
	texts := assistant.Texts(tr)
	menu = gio.NewMenu()
	actions := gio.NewMenu()
	for _, act := range assistant.MessageActions {
		item := gio.NewMenuItem(assistant.Label(tr, act), "")
		item.SetActionAndTargetValue(prefix+".assistant", glib.NewVariantString(string(act)))
		actions.AppendItem(item)
	}
	menu.AppendSection("", actions)
	if unread {
		folder := gio.NewMenu()
		folder.Append(assistant.Label(tr, assistant.Unread), "win.assistant-unread")
		menu.AppendSection("", folder)
	}
	targets := gio.NewMenu()
	for _, t := range assistantTargets {
		action := "app.assistant-target"
		if !supportedTarget(t) {
			action = "app.assistant-unsupported"
		}
		item := gio.NewMenuItem(assistant.TargetName(tr, t), "")
		item.SetActionAndTargetValue(action, glib.NewVariantString(string(t)))
		targets.AppendItem(item)
	}
	menu.AppendSection(texts.OpenIn, targets)
	setup := gio.NewMenu()
	menu.AppendSection("", setup)
	update = func() {
		setup.RemoveAll()
		target, ok := a.pick(true)
		if ok {
			return
		}
		setup.Append(a.problem(target), "app.assistant-problem")
		setup.Append(texts.SetUp, "app.assistant-setup")
	}
	update()
	return menu, update
}

// bindAssistantButton makes b the Assistant button: its menu (with unread,
// Summarize Unread in This Folder) on the prefix.* actions, visible while
// the Assistant is shown. Opening it asks for the state again (the menu
// uses the last known one) and calls opening first, which brings the
// window's actions up to date. The returned function stops following.
func (a *Assistant) bindAssistantButton(b *gtk.MenuButton, prefix string, unread bool, opening func()) (unbind func()) {
	menu, update := a.assistantMenu(prefix, unread)
	b.SetMenuModel(menu)
	b.SetIconName(assistantIcon)
	b.SetTooltipText(assistant.Texts(tr).Assistant)
	b.SetCreatePopupFunc(func(*gtk.MenuButton) {
		a.Refresh()
		opening()
		update()
	})
	sync := func() {
		b.SetVisible(a.shown())
		update()
		opening()
	}
	sync()
	return a.OnChange(sync)
}

// newestFirst is the ids of a row, newest first, as a prompt takes them: a
// conversation's members come oldest first (rowIDs), a message's one id as
// it is.
func newestFirst(ids []api.MessageID, thread bool) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		if thread {
			out[len(ids)-1-i] = string(id)
		} else {
			out[i] = string(id)
		}
	}
	return out
}

// askAssistant runs a message action on the list's selection: a
// conversation row's folder members (fetched first when not known yet), or
// the one message. Never an Outbox message: it is not on the server yet.
func (w *Window) askAssistant(act assistant.Action) {
	w.selectedIDs(func(row listRow, ids []api.MessageID) {
		if w.model.inOutbox(row.Message) {
			return
		}
		w.handOff(&w.ApplicationWindow.Window, act, row.Message.AccountID, newestFirst(ids, row.Thread), w.Toast)
	})
}

// handOff opens the chosen Claude app with the prompt of a message action
// on ids (newest first) of account acc; toast says what failed.
func (w *Window) handOff(parent *gtk.Window, act assistant.Action, acc api.AccountID, ids []string, toast func(string)) {
	target, ok := w.assist.pick(true)
	if !ok {
		return
	}
	prompt, err := assistant.Prompt(tr, target, act, assistant.Selection{AccountID: string(acc), MessageIDs: ids})
	if err != nil {
		w.assistantFailed(err, toast)
		return
	}
	w.openAssistantLink(parent, assistant.Link(target, prompt), toast)
}

// canSummarizeUnread says whether Summarize Unread in This Folder can run
// on the folder selected in the sidebar: there is one, it is not an Outbox
// (whose messages are not on the server), and no search replaces it.
func (w *Window) canSummarizeUnread() bool {
	k := w.model.selected
	if k.Account == "" || k.Folder == "" || w.model.search.active {
		return false
	}
	return w.model.folderRole(k) != api.RoleOutbox
}

// summarizeUnread is Summarize Unread in This Folder for the folder
// selected in the sidebar.
func (w *Window) summarizeUnread() {
	if !w.canSummarizeUnread() {
		return
	}
	target, ok := w.assist.pick(true)
	if !ok {
		return
	}
	k := w.model.selected
	prompt, err := assistant.UnreadPrompt(tr, string(k.Account), string(k.Folder))
	if err != nil {
		w.assistantFailed(err, w.Toast)
		return
	}
	w.openAssistantLink(&w.ApplicationWindow.Window, assistant.Link(target, prompt), w.Toast)
}

// syncAssistantActions enables the main window's Assistant actions: a
// message action while a message not in the Outbox is selected, Summarize
// Unread while its folder can be summarised, both only while the chosen
// target can run them (the menu then says why not instead).
func (w *Window) syncAssistantActions() {
	_, ok := w.assist.pick(true)
	row, selected := w.selectedRow()
	if a := w.actions["assistant"]; a != nil {
		a.SetEnabled(ok && selected && !w.model.inOutbox(row.Message))
	}
	if a := w.actions["assistant-unread"]; a != nil {
		a.SetEnabled(ok && w.canSummarizeUnread())
	}
}

// openAssistantLink hands a claude:// or claude-cli:// link to the app
// that handles it. The link of a file names the attachment, so it is never
// logged.
func (w *Window) openAssistantLink(parent *gtk.Window, link string, toast func(string)) {
	widget.LaunchURI(parent, link, func(err error) {
		if err != nil {
			w.log.Warn("opening an assistant link", "err", err)
			toast(widget.LaunchErrorText(err))
		}
	})
}

// assistantFailed reports a prompt or a link that could not be built: ids
// missing, a prompt too long even for one message, a path that is not
// clean.
func (w *Window) assistantFailed(err error, toast func(string)) {
	w.log.Warn("assistant", "err", err)
	toast(widget.LaunchErrorText(err))
}

// askAboutAttachment is an attachment's "Ask the Assistant…": the part
// fetched (the message downloaded first when remote) and written like
// Open writes it, then handed to Claude Code, which needs its handler but
// not the bridge (Claude reads the file, not the mail), with the file's
// private directory as its working directory. Failures of the fetch and
// the write have their toasts.
func (v *messageView) askAboutAttachment(acc api.AccountID, id api.MessageID, a api.Attachment, remote bool) {
	w := v.win
	w.assist.refreshHandlers()
	if _, ok := w.assist.pick(false); !ok {
		return
	}
	v.writeAttachment(acc, id, a, remote, func(path string, _ bool) {
		// Asked again: the download may have taken a while.
		target, ok := w.assist.pick(false)
		if !ok {
			return
		}
		link, err := assistant.FileLink(target, path, assistant.FilePrompt(tr, target))
		if err != nil {
			w.assistantFailed(err, v.say)
			return
		}
		w.openAssistantLink(v.parent, link, v.say)
	})
}

// bindAskItem keeps a chip's "Ask the Assistant…" (att.ask in group g)
// with the Assistant: the action exists while the Assistant is shown (the
// item hides without it) and is enabled while the chosen Claude app is
// installed. The arrow looks again as its menu opens.
func (v *messageView) bindAskItem(arrow *gtk.MenuButton, g *gio.SimpleActionGroup, ask *gio.SimpleAction) {
	a := v.win.assist
	added := false
	sync := func() {
		switch shown := a.shown(); {
		case shown && !added:
			g.AddAction(ask)
		case !shown && added:
			g.RemoveAction("ask")
		}
		added = a.shown()
		_, ok := a.pick(false)
		ask.SetEnabled(ok)
	}
	sync()
	arrow.SetCreatePopupFunc(func(*gtk.MenuButton) {
		a.refreshHandlers()
		sync()
	})
}
