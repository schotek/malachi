// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/mcpsetup"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The settings of the Claude apps on this computer (Preferences →
// Integrations → Claude Code / Claude Desktop, its Settings and the
// catalogue's Add…; data/ui/claude_integration.blp): the MCP switch,
// which shows what malachi-mcp reports about the Claude configuration
// files and registers or unregisters the bridge (bindMCP), and the
// Claude Code row the assistant panel runs, with Choose…, Sign In… and
// Get Claude Code… (bindClaudeCode). Both were rows of Preferences → AI
// until the AI providers through their API arrived (docs/plans/ai-api.md
// §7.2); their logic moved here unchanged.
type claudeIntegration struct {
	*adw.Dialog

	assist *Assistant
	log    *slog.Logger
	toasts *adw.ToastOverlay
	closed bool

	mcpGroup      *adw.PreferencesGroup
	mcpSwitch     *adw.SwitchRow
	claudeGroup   *adw.PreferencesGroup
	claudeCodeRow *adw.ActionRow
	claudeChoose  *gtk.Button
}

// newClaudeIntegration builds the dialog over the application's Assistant
// state and the settings store; Present it with Present(parent).
func newClaudeIntegration(s *settings.Store, as *Assistant, log *slog.Logger) *claudeIntegration {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	b := data.Builder("claude_integration.ui")
	d := &claudeIntegration{
		Dialog:        b.GetObject("claude_integration_dialog").Cast().(*adw.Dialog),
		assist:        as,
		log:           log.With("component", "claude-integration"),
		toasts:        b.GetObject("toast_overlay").Cast().(*adw.ToastOverlay),
		mcpGroup:      b.GetObject("mcp_group").Cast().(*adw.PreferencesGroup),
		mcpSwitch:     b.GetObject("mcp_switch").Cast().(*adw.SwitchRow),
		claudeGroup:   b.GetObject("claude_code_group").Cast().(*adw.PreferencesGroup),
		claudeCodeRow: b.GetObject("assistant_claude_code").Cast().(*adw.ActionRow),
		claudeChoose:  b.GetObject("assistant_claude_choose").Cast().(*gtk.Button),
	}
	d.claudeCodeRow.SetTitle(assistant.TargetName(tr, assistant.Code))
	d.claudeChoose.SetLabel(assistant.PanelTexts(tr).Choose)
	cleanup := []func(){d.bindMCP()}
	show, unbindClaude := d.bindClaudeCode(s)
	cleanup = append(cleanup, unbindClaude)
	// Another claude chosen: its row looks again.
	cleanup = append(cleanup, s.OnChanged(settings.KeyAssistantClaudePath, func() {
		if !d.closed {
			show()
		}
	}))
	// A Claude app may have been installed, or Claude Code signed in,
	// meanwhile.
	as.locator.Refresh()
	show()
	d.ConnectClosed(func() {
		d.closed = true
		for _, f := range cleanup {
			f()
		}
	})
	return d
}

// AddToast shows a toast over the dialog.
func (d *claudeIntegration) AddToast(t *adw.Toast) { d.toasts.AddToast(t) }

// editClaudeIntegration opens the Claude integration's settings over the
// preferences.
func (d *PreferencesDialog) editClaudeIntegration() {
	if d.assist == nil || d.settings == nil {
		return
	}
	newClaudeIntegration(d.settings, d.assist, d.log).Present(d)
}

// bindMCP drives the "Register with Claude" switch through the malachi-mcp
// bridge (status / install / uninstall). The Claude configuration files
// are the only state: the switch shows what the bridge reports, stays
// insensitive while a call runs and reverts when the change fails. The
// application's last status is shown at once, so the switch does not show
// "off" only because the dialog has not asked yet, and so is every newer
// one it learns while no call of the dialog runs; every status the dialog
// gets goes to the application (Assistant.Apply), whose menus follow it. A
// failed status check is asked again after 1, 2 and 4 s.
func (d *claudeIntegration) bindMCP() (unbind func()) {
	row := d.mcpSwitch
	var (
		syncing bool // set while the switch is updated programmatically
		// op is bumped by every call; the reply of an older one is dropped.
		op       int
		changing bool // an install or uninstall runs
	)
	set := func(v bool) {
		syncing = true
		row.SetActive(v)
		syncing = false
	}
	row.SetSensitive(false)

	// Inside the Flatpak sandbox the bridge could neither see the Claude
	// apps' files nor be started by them; the group says so (bindMail
	// reports its failures in the same place).
	if os.Getenv("FLATPAK_ID") != "" {
		d.mcpGroup.SetDescription(i18n.T("Not available in the Flatpak build"))
		return func() {}
	}
	bridge, err := mcpsetup.Locate()
	if err != nil {
		d.log.Warn("locating the MCP bridge", "err", err)
		d.mcpGroup.SetDescription(i18n.T("The MCP bridge (malachi-mcp) was not found"))
		return func() {}
	}

	a := d.assist
	follow := func() {
		if d.closed || changing || !a.known() {
			return
		}
		set(a.registered())
		row.SetSensitive(true)
	}
	follow()
	removeFollow := a.OnChange(follow)

	// A failed status check is repeated after each of the pauses (a Claude
	// app may be rewriting its file just then), unless a newer call came
	// meanwhile; the last known state stays shown. Only when the repeats
	// are used up and no state is known does the group say why.
	retries := 0
	var query func()
	query = func() {
		op++
		my := op
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), mcpsetup.Timeout)
			defer cancel()
			st, err := mcpsetup.Query(ctx, bridge)
			glib.IdleAdd(func() {
				if d.closed || my != op {
					return
				}
				if err != nil {
					d.log.Warn("malachi-mcp status", "err", err, "retry", retries < len(mcpStatusRetryDelays))
					if retries < len(mcpStatusRetryDelays) {
						delay := mcpStatusRetryDelays[retries]
						retries++
						glib.TimeoutAdd(uint(delay.Milliseconds()), func() bool {
							if !d.closed && my == op {
								query()
							}
							return false
						})
						return
					}
					if !a.known() {
						// TRANSLATORS: %s is a one-line reason from the malachi-mcp bridge.
						d.mcpGroup.SetDescription(fmt.Sprintf(i18n.T("The MCP bridge did not answer: %s"), err))
					}
					return
				}
				retries = 0
				a.Apply(st)
				set(st.Registered())
				row.SetSensitive(true)
			})
		}()
	}
	query()

	handle := row.NotifyProperty("active", func() {
		if syncing {
			return
		}
		want := row.Active()
		row.SetSensitive(false)
		op++
		my := op
		changing = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), mcpsetup.Timeout)
			defer cancel()
			var (
				st  mcpsetup.Status
				err error
			)
			if want {
				st, err = mcpsetup.Install(ctx, bridge)
			} else {
				st, err = mcpsetup.Uninstall(ctx, bridge)
			}
			glib.IdleAdd(func() {
				if d.closed || my != op {
					return
				}
				changing = false
				row.SetSensitive(true)
				if err != nil {
					d.log.Warn("changing the MCP registration", "register", want, "err", err)
					set(!want)
					switch {
					case errors.Is(err, mcpsetup.ErrNoClient):
						d.AddToast(widget.PlainToast(i18n.T("No Claude app was found on this computer")))
					case want:
						// TRANSLATORS: %s is a one-line reason from the malachi-mcp bridge.
						d.AddToast(widget.PlainToast(fmt.Sprintf(i18n.T("The MCP bridge could not be registered: %s"), err)))
					default:
						// TRANSLATORS: %s is a one-line reason from the malachi-mcp bridge.
						d.AddToast(widget.PlainToast(fmt.Sprintf(i18n.T("The MCP bridge could not be unregistered: %s"), err)))
					}
					return
				}
				// The bridge's report is authoritative; it may differ from
				// what was asked for (e.g. one client left registered).
				retries = 0
				a.Apply(st)
				set(st.Registered())
			})
		}()
	})
	return func() {
		removeFollow()
		row.HandlerDisconnect(handle)
	}
}

// bindClaudeCode drives the Claude Code row: show fills its subtitle with
// the executable the panel runs, its version and whether it is signed in
// (asked once, then kept by the locator until the dialog opens again, the
// path changes or a sign-in starts or ends), or that none was found;
// "Choose…" picks one of the user's own (assistant-claude-path). The file
// found automatically stores nothing, so choosing it goes back to looking
// in the usual places.
//
// A second button, made here, is what the row offers: "Sign In…" while
// Claude Code says it is signed out, which runs Claude Code's own sign-in
// in the browser (the locator's, shared with the panel: the row says
// "Waiting for the sign-in in your browser…" whoever started it, and a
// second click starts it afresh), and "Get Claude Code…" while there is
// none, which opens Anthropic's page with the installers.
func (d *claudeIntegration) bindClaudeCode(s *settings.Store) (show func(), unbind func()) {
	a := d.assist
	offer := assistantpanel.OfferNone
	button := gtk.NewButtonWithLabel("")
	button.SetVAlign(gtk.AlignCenter)
	button.SetVisible(false)
	d.claudeCodeRow.AddSuffix(button)
	setOffer := func(o assistantpanel.Offer) {
		offer = o
		button.SetLabel(offerLabel(o))
		button.SetVisible(o != assistantpanel.OfferNone)
	}
	gen := 0
	show = func() {
		gen++
		my := gen
		path := a.locator.Locate()
		if path == "" {
			d.claudeCodeRow.SetSubtitle(assistant.Problem(tr, assistant.App, assistant.Availability{}))
			setOffer(assistantpanel.OfferInstall)
			return
		}
		if !strings.HasPrefix(d.claudeCodeRow.Subtitle(), path) {
			d.claudeCodeRow.SetSubtitle(path)
			setOffer(assistantpanel.OfferNone)
		}
		a.locator.Version(func(version string) {
			a.locator.SignedIn(func(in assistantpanel.SignIn) {
				if d.closed || my != gen {
					return
				}
				d.claudeCodeRow.SetSubtitle(claudeCodeState(path, version, in, a.locator.SigningIn()))
				if in.Known && !in.SignedIn {
					setOffer(assistantpanel.OfferSignIn)
				} else {
					setOffer(assistantpanel.OfferNone)
				}
			})
		})
	}
	offered := button.ConnectClicked(func() {
		switch offer {
		case assistantpanel.OfferInstall:
			// The preferences are a dialog, not a window: no parent.
			widget.LaunchURI(nil, assistant.InstallURL, func(err error) {
				if err != nil && !d.closed {
					d.AddToast(widget.PlainToast(widget.LaunchErrorText(err)))
				}
			})
		case assistantpanel.OfferSignIn:
			a.locator.SignIn(func(r assistantpanel.SignInResult) {
				if d.closed {
					return
				}
				switch r.Outcome {
				case assistantpanel.SignInFailed:
					d.AddToast(widget.PlainToast(assistant.SignInFailedText(tr, r.Reason)))
				case assistantpanel.SignInTimedOut:
					d.AddToast(widget.PlainToast(assistant.SignInTexts(tr).TimedOut))
				}
			})
		}
	})
	// A sign-in started or ended, here or in the panel: the row looks again.
	unwatch := a.locator.OnSignInChange(func() {
		if !d.closed {
			show()
		}
	})
	handle := d.claudeChoose.ConnectClicked(func() {
		dlg := gtk.NewFileDialog()
		dlg.SetTitle(assistant.TargetName(tr, assistant.Code))
		if current := a.locator.Locate(); current != "" {
			dlg.SetInitialFolder(gio.NewFileForPath(filepath.Dir(current)))
		}
		// The preferences are a dialog, not a window: no parent.
		dlg.Open(context.Background(), nil, func(r gio.AsyncResulter) {
			f, err := dlg.OpenFinish(r)
			if err != nil || d.closed {
				return // dismissed
			}
			chosen := f.Path()
			if chosen == "" || !assistantpanel.IsExecutableFile(chosen) {
				return
			}
			value := chosen
			if chosen == a.locator.AutomaticPath() {
				value = ""
			}
			a.locator.Refresh()
			if s.AssistantClaudePath() == value {
				show()
				return
			}
			// The change handlers look again.
			s.SetAssistantClaudePath(value)
		})
	})
	return show, func() {
		d.claudeChoose.HandlerDisconnect(handle)
		button.HandlerDisconnect(offered)
		unwatch()
	}
}

// claudeCodeState is the Claude Code row's subtitle: "path · version ·
// Signed in"; what is not known is left out, and while a sign-in is under
// way (signingIn) the row says that it waits for the browser instead.
func claudeCodeState(path, version string, in assistantpanel.SignIn, signingIn bool) string {
	t := assistant.PanelTexts(tr)
	parts := []string{path}
	if version != "" {
		parts = append(parts, version)
	}
	switch {
	case in.Known && in.SignedIn:
		parts = append(parts, t.SignedIn)
	case signingIn:
		parts = append(parts, assistant.SignInTexts(tr).Waiting)
	case in.Known:
		parts = append(parts, t.NotSignedInShort)
	}
	return strings.Join(parts, " · ")
}
