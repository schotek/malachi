// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package editor is the rich-text editor of the compose window: a
// WebKitGTK 6.0 view showing a contenteditable document, driven by WebKit's
// native editing commands and observed through a small user script.
//
// It knows nothing about mail. Callers hand it body HTML that is already
// safe for the page (escaped quotes, backend-sanitised drafts) and read the
// body's HTML and text back; the backend sanitises whatever is sent.
package editor

import (
	"log/slog"
	"strconv"

	"github.com/diamondburned/gotk4-webkitgtk/pkg/javascriptcore/v6"
	"github.com/diamondburned/gotk4-webkitgtk/pkg/webkit/v6"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
	_ "github.com/schotek/malachi/ui/internal/webkitenv" // renderer switch before the first view
)

// Editor is the WebView plus the bridge state. Embed it where a widget is
// expected.
type Editor struct {
	*webkit.WebView

	ucm *webkit.UserContentManager
	log *slog.Logger

	ready   bool
	html    string // last body innerHTML seen (or loaded)
	text    string // last body innerText seen
	waiters []func()
	// rewrites wait for the page's "rewrite" message (RewriteTarget).
	rewrites []func(RewriteTarget)

	// OnChanged fires after the page reported new content (debounced).
	OnChanged func()
	// OnState fires when the formatting at the caret changed.
	OnState func(State)
	// OnReady fires once the bridge is running after a Load.
	OnReady func()
	// OnCrashed fires when the web process died; the view is blank until
	// Load is called again.
	OnCrashed func()
	// OnDropFiles receives files dropped onto the view; WebKit never sees
	// them. Other drops (text, a picture from a page) are WebKit's editing.
	OnDropFiles func(files []*gio.File)
	// OnPaste receives pasted plain text that looks like Markdown (bridgeJS)
	// and must call answer once, on the main loop, with the HTML to insert
	// in its place or "" for the text as it is. Unset: the text is pasted.
	OnPaste func(text string, answer func(html string))
}

// New builds an editor from data/ui/editor.blp. Call Load to show content.
func New(log *slog.Logger) *Editor {
	registerCIDScheme()

	b := data.Builder("editor.ui")
	e := &Editor{
		WebView: b.GetObject("editor_view").Cast().(*webkit.WebView),
		ucm:     b.GetObject("editor_ucm").Cast().(*webkit.UserContentManager),
		log:     log.With("component", "editor"),
	}

	// Bridge: the handler must exist before the document loads.
	e.ucm.RegisterScriptMessageHandler("malachi", "")
	e.ucm.ConnectScriptMessageReceived(e.onMessage)
	e.ucm.AddScript(webkit.NewUserScript(bridgeJS,
		webkit.UserContentInjectTopFrame, webkit.UserScriptInjectAtDocumentEnd, nil, nil))

	// No navigation away from our document, no new windows, no WebKit
	// context menu (it would offer Reload and Open Link).
	e.ConnectDecidePolicy(e.decidePolicy)
	e.ConnectContextMenu(func(*webkit.ContextMenu, *webkit.HitTestResult) bool { return true })
	e.ConnectLoadChanged(func(ev webkit.LoadEvent) {
		if ev == webkit.LoadFinished {
			e.log.Debug("document loaded")
		}
	})
	e.ConnectWebProcessTerminated(func(reason webkit.WebProcessTerminationReason) {
		e.ready = false
		e.answerRewrites(RewriteTarget{})
		if reason == webkit.WebProcessTerminatedByApi {
			return // Close ended it
		}
		e.log.Warn("web process terminated", "reason", int(reason))
		if e.OnCrashed != nil {
			e.OnCrashed()
		}
	})

	// A file drop is the caller's business (attachments): WebKit would
	// insert or navigate to the file: URL. Capture phase, so this target
	// decides before WebKit's own; it takes file lists only.
	drop := gtk.NewDropTarget(gdk.GTypeFileList, gdk.ActionCopy)
	drop.SetPropagationPhase(gtk.PhaseCapture)
	drop.ConnectDrop(func(value *coreglib.Value, _, _ float64) bool {
		list, ok := value.GoValue().(*gdk.FileList)
		if !ok || e.OnDropFiles == nil {
			return false
		}
		files := list.Files()
		if len(files) == 0 {
			return false
		}
		e.OnDropFiles(files)
		return true
	})
	e.AddController(drop)
	return e
}

// Close ends the editor's web process at once, for an editor whose window
// closes: without it the process lives on until the Go wrapper of the view
// is collected, and compose windows opened and closed one after another
// pile processes up until the sandbox cannot start another one. The
// editor is dead afterwards.
func (e *Editor) Close() {
	e.OnCrashed, e.OnChanged, e.OnState, e.OnReady, e.OnDropFiles, e.OnPaste = nil, nil, nil, nil, nil, nil
	e.ready = false
	e.waiters = nil
	e.StopLoading()
	e.TerminateWebProcess()
}

// Load replaces the document with bodyHTML (already safe for the page).
func (e *Editor) Load(bodyHTML string) {
	e.ready = false
	e.html, e.text = bodyHTML, ""
	e.LoadHtml(Document(bodyHTML), "")
}

// HTML is the body's last known innerHTML.
func (e *Editor) HTML() string { return e.html }

// Text is the body's last known innerText.
func (e *Editor) Text() string { return e.text }

// Ready reports whether the bridge is running.
func (e *Editor) Ready() bool { return e.ready }

// Flush asks the page for its current content and calls done (on the main
// loop) once a fresh "changed" message has arrived, or immediately when the
// bridge is not running.
func (e *Editor) Flush(done func()) {
	if !e.ready {
		done()
		return
	}
	e.waiters = append(e.waiters, done)
	e.eval("window.malachi.flush()")
}

// Exec runs a WebKit editing command (Bold, InsertUnorderedList,
// FormatBlock, CreateLink, ForeColor, InsertImage, …) on the current
// selection. Ignored until the document is ready.
func (e *Editor) Exec(command, argument string) {
	if !e.ready {
		return
	}
	if argument == "" {
		e.ExecuteEditingCommand(command)
	} else {
		e.ExecuteEditingCommandWithArgument(command, argument)
	}
}

// FocusStart focuses the body with the caret at its beginning.
func (e *Editor) FocusStart() {
	e.GrabFocus()
	if e.ready {
		e.eval("window.malachi.focusStart()")
	}
}

// RewriteTarget notes the passage the assistant's rewrite works on and
// calls done (on the main loop) with it: the selection, or the user's own
// text, everything above the line attribution (the whole body when ""
// or not found; bridgeJS). done gets an empty target when the bridge is
// not running.
func (e *Editor) RewriteTarget(attribution string, done func(RewriteTarget)) {
	if !e.ready {
		done(RewriteTarget{})
		return
	}
	e.rewrites = append(e.rewrites, done)
	e.eval("window.malachi.rewriteTarget(" + jsString(attribution) + ")")
}

// ApplyRewrite puts text in place of the passage RewriteTarget noted, or
// below it, as plain text (RewriteInsertion): one step the page's undo
// takes back, reported as typing is. The editor should have the keyboard.
func (e *Editor) ApplyRewrite(text string, below bool) {
	if !e.ready {
		return
	}
	command, argument := RewriteInsertion(text, below)
	flag := "false"
	if below {
		flag = "true"
	}
	e.eval("window.malachi.rewriteApply(" + flag + ", " + jsString(command) + ", " + jsString(argument) + ")")
}

func (e *Editor) answerRewrites(t RewriteTarget) {
	waiting := e.rewrites
	e.rewrites = nil
	for _, done := range waiting {
		done(t)
	}
}

// SetDebug forwards the page's console output to stderr.
func (e *Editor) SetDebug(on bool) {
	e.Settings().SetEnableWriteConsoleMessagesToStdout(on)
}

func (e *Editor) onMessage(v *javascriptcore.Value) {
	if !v.IsString() {
		return
	}
	msg, err := decodeMessage(v.String())
	if err != nil {
		e.log.Warn("bad bridge message", "err", err)
		return
	}
	switch msg.Type {
	case "ready":
		e.ready = true
		e.log.Debug("bridge ready")
		if e.OnReady != nil {
			e.OnReady()
		}
	case "changed":
		e.html, e.text = msg.HTML, msg.Text
		e.log.Debug("content changed", "seq", msg.Seq, "bytes", len(msg.HTML))
		waiters := e.waiters
		e.waiters = nil
		for _, done := range waiters {
			done()
		}
		if e.OnChanged != nil {
			e.OnChanged()
		}
	case "state":
		if e.OnState != nil {
			e.OnState(msg.State)
		}
	case "rewrite":
		e.answerRewrites(RewriteTarget{Selected: msg.Selected, Text: msg.Text})
	case "paste":
		e.paste(msg.ID, msg.Text)
	}
}

// paste hands a held-back paste to OnPaste and its answer to the page; a
// page loaded meanwhile ignores the id.
func (e *Editor) paste(id int, text string) {
	answer := func(html string) {
		if !e.ready {
			return
		}
		arg := "null"
		if html != "" {
			arg = jsString(html)
		}
		e.eval("window.malachi.pasted(" + strconv.Itoa(id) + ", " + arg + ")")
	}
	if e.OnPaste == nil {
		answer("")
		return
	}
	e.OnPaste(text, answer)
}

// decidePolicy allows only our own document load; every other navigation
// and every new-window request is refused. Resource responses are allowed
// (the CSP already limits them to cid: and data: images).
func (e *Editor) decidePolicy(decision webkit.PolicyDecisioner, t webkit.PolicyDecisionType) bool {
	base := webkit.BasePolicyDecision(decision)
	switch t {
	case webkit.PolicyDecisionTypeNavigationAction:
		nav, ok := decision.(*webkit.NavigationPolicyDecision)
		if !ok {
			e.log.Warn("navigation decision of unexpected type; allowing")
			base.Use()
			return true
		}
		action := nav.NavigationAction()
		uri := action.Request().URI()
		if uri == "about:blank" && !action.IsUserGesture() {
			base.Use()
		} else {
			e.log.Debug("navigation refused", "uri", uri)
			base.Ignore()
		}
	case webkit.PolicyDecisionTypeNewWindowAction:
		base.Ignore()
	default:
		base.Use()
	}
	return true
}
