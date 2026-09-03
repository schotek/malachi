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

	"github.com/diamondburned/gotk4-webkitgtk/pkg/javascriptcore/v6"
	"github.com/diamondburned/gotk4-webkitgtk/pkg/webkit/v6"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
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

	// OnChanged fires after the page reported new content (debounced).
	OnChanged func()
	// OnState fires when the formatting at the caret changed.
	OnState func(State)
	// OnReady fires once the bridge is running after a Load.
	OnReady func()
	// OnCrashed fires when the web process died; the view is blank until
	// Load is called again.
	OnCrashed func()
}

// New builds an editor from data/ui/editor.blp. Call Load to show content.
func New(log *slog.Logger) *Editor {
	registerCIDScheme()

	b := gtk.NewBuilderFromString(data.MustUI("editor.ui"))
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
		e.log.Warn("web process terminated", "reason", int(reason))
		if e.OnCrashed != nil {
			e.OnCrashed()
		}
	})
	return e
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
	}
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
