// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/chatgpt"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

func (a *Assistant) usesChatGPT() bool { return a.settings.AssistantProvider() == "chatgpt" }

func (a *Assistant) initChatGPT() {
	if os.Getenv("FLATPAK_ID") == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			if store, err := chatgpt.NewNativeStore(filepath.Join(dir, "malachi", "chatgpt")); err == nil {
				a.chatGPT = chatgpt.NewConnectionService(nil, store, chatgpt.NewBrowser(func(url string) error {
					result := make(chan error, 1)
					glib.IdleAdd(func() {
						if a.chatGPTClosed {
							result <- errors.New("chatgpt_closed")
							return
						}
						widget.LaunchURI(nil, url, func(err error) { result <- err })
					})
					select {
					case err := <-result:
						return err
					case <-time.After(30 * time.Second):
						return errors.New("chatgpt_browser_timeout")
					}
				}))
				a.chatGPT.Observe(func(c chatgpt.Connection) {
					glib.IdleAdd(func() {
						if a.chatGPTClosed {
							return
						}
						if c.Status != a.chatGPTState.Status || c.ConnectionID != a.chatGPTState.ConnectionID {
							a.chatGPTState = c
							a.runtimeChanged()
						} else {
							a.notify()
						}
					})
				})
				go func() { _ = a.chatGPT.Initialize(context.Background()) }()
			}
		}
	}
	a.rebuildCodex()
	for _, key := range []string{settings.KeyAssistantProvider, settings.KeyAssistantCodexPath,
		settings.KeyAssistantChatGPTModel, settings.KeyAssistantTarget} {
		key := key
		a.settings.OnChanged(key, func() {
			a.runtimeChanged()
			if key == settings.KeyAssistantProvider && a.board != nil {
				// Automatic mail transfer never silently follows a provider
				// switch, and the daemon's assistant preference stays only
				// with the new provider's board consent: one quiet write
				// (boardtriage.Controller.ProviderChanged).
				a.board.ctl.ProviderChanged(true)
			}
		})
	}
	a.settings.OnChanged(settings.KeyAssistantChatGPTConsentVersion, func() {
		if a.settings.AssistantChatGPTConsentVersion() != 1 {
			a.runtimeChanged()
		} else {
			a.notify()
		}
	})
	a.settings.OnChanged(settings.KeyBoardChatGPTConsentVersion, func() {
		if a.board != nil {
			a.board.ctl.SettingsChanged()
		}
	})
	a.settings.OnChanged(settings.KeyBoardChatGPTModel, a.runtimeChanged)
}

func (a *Assistant) rebuildCodex() {
	if a.chatGPT == nil {
		a.codex = nil
		return
	}
	a.codex = chatgpt.NewProvider(chatgpt.Options{
		Executable: func() string { return chatgpt.Locate(a.settings.AssistantCodexPath()) },
		Bridge:     a.bridge, Socket: a.chatGPTSocket, Directory: filepath.Join(assistantDirectory(), "chatgpt"), Env: os.Environ(),
		Model:         a.settings.AssistantChatGPTModel,
		HasConsent:    func() bool { return a.settings.AssistantChatGPTConsentVersion() == 1 },
		AcceptConsent: func() { a.settings.SetAssistantChatGPTConsentVersion(1) },
	}, a.chatGPT)
	_ = a.codex.CleanAbandonedSessions()
}

func (a *Assistant) runtimeChanged() {
	a.runtimeGeneration++
	a.rebuildCodex()
	if a.board != nil {
		a.board.ctl.Cancel()
		a.board.ctl.SettingsChanged()
		a.board.ctl.AvailabilityChanged()
		a.board.ctl.CheckSignIn()
	}
	if a.reply != nil {
		a.reply.ctl.Cancel()
		a.reply.ctl.AvailabilityChanged()
		a.reply.ctl.CheckSignIn()
	}
	a.RefreshHandlers()
	a.notify()
}

// Provider is the active in-app runtime; nil preserves Claude.
func (a *Assistant) Provider() assistantpanel.Provider {
	if a.usesChatGPT() {
		if a.codex != nil {
			return a.codex
		}
		return unavailableChatGPT{}
	}
	return nil
}

type unavailableChatGPT struct{}

func (unavailableChatGPT) Model() string    { return "" }
func (unavailableChatGPT) HasConsent() bool { return false }
func (unavailableChatGPT) AcceptConsent()   {}
func (unavailableChatGPT) Open(context.Context, assistantpanel.SessionSpec) (assistantpanel.Session, error) {
	return nil, errors.New("chatgpt_unavailable")
}

// RuntimeGeneration changes when an active request must be discarded.
func (a *Assistant) RuntimeGeneration() int { return a.runtimeGeneration }

func (a *Assistant) chatGPTReady() bool {
	return a.chatGPT != nil && a.chatGPT.IsReady() && chatgpt.Locate(a.settings.AssistantCodexPath()) != ""
}

// HasAssistantConsent keeps each provider's disclosure separate.
func (a *Assistant) HasAssistantConsent() bool { return providerSettings{a}.AssistantConsent() }

// EnsureAssistantConsent is used before opening a compose popover.
func (a *Assistant) EnsureAssistantConsent(parent gtk.Widgetter, done func(bool)) {
	if a.HasAssistantConsent() {
		done(true)
		return
	}
	a.AskAssistantConsent(parent, func(ok bool) {
		if ok {
			providerSettings{a}.SetAssistantConsent(true)
		}
		done(ok)
	})
}

// AskAssistantConsent rejects approvals from a stale provider/model/account.
func (a *Assistant) AskAssistantConsent(parent gtk.Widgetter, done func(bool)) {
	generation := a.runtimeGeneration
	finish := func(ok bool) { done(ok && generation == a.runtimeGeneration && !a.chatGPTClosed) }
	if a.usesChatGPT() {
		t := assistant.ChatGPTText(tr)
		widget.AskProviderConsent(parent, t.ConsentHeading, t.ConsentBody, finish)
	} else {
		widget.AskAssistantConsent(parent, finish)
	}
}

func (a *Assistant) askTriageConsent(parent gtk.Widgetter, done func(bool)) {
	generation := a.runtimeGeneration
	finish := func(ok bool) { done(ok && generation == a.runtimeGeneration && !a.chatGPTClosed) }
	if a.usesChatGPT() {
		t := assistant.ChatGPTText(tr)
		widget.AskProviderConsent(parent, t.BoardConsentHeading, t.BoardConsentBody, finish)
	} else {
		widget.AskTriageConsent(parent, finish)
	}
}

// CloseChatGPT cancels authorization and every gateway through its session context.
func (a *Assistant) CloseChatGPT() {
	a.chatGPTClosed = true
	if a.chatGPT != nil {
		a.chatGPT.Close()
	}
}

// Provider-specific settings adapt existing pure Board controllers.
type providerSettings struct{ a *Assistant }

func (s providerSettings) AssistantConsent() bool {
	if s.a.usesChatGPT() {
		return s.a.settings.AssistantChatGPTConsentVersion() == 1
	}
	return s.a.settings.AssistantConsent()
}
func (s providerSettings) SetAssistantConsent(ok bool) {
	if s.a.usesChatGPT() {
		v := 0
		if ok {
			v = 1
		}
		s.a.settings.SetAssistantChatGPTConsentVersion(v)
	} else {
		s.a.settings.SetAssistantConsent(ok)
	}
}
func (s providerSettings) AssistantClaudePath() string     { return s.a.settings.AssistantClaudePath() }
func (s providerSettings) AssistantModel() assistant.Model { return s.a.settings.AssistantModel() }
func (s providerSettings) BoardTriageConsent() bool {
	if s.a.usesChatGPT() {
		return s.a.settings.BoardChatGPTConsentVersion() == 1
	}
	return s.a.settings.BoardTriageConsent()
}
func (s providerSettings) SetBoardTriageConsent(ok bool) {
	if s.a.usesChatGPT() {
		v := 0
		if ok {
			v = 1
		}
		s.a.settings.SetBoardChatGPTConsentVersion(v)
	} else {
		s.a.settings.SetBoardTriageConsent(ok)
	}
}
func (s providerSettings) BoardTriageModel() assistant.Model { return s.a.settings.BoardTriageModel() }

type providerLocator struct{ a *Assistant }

func (l providerLocator) Locate() string {
	if l.a.usesChatGPT() {
		if l.a.codex == nil {
			return ""
		}
		return chatgpt.Locate(l.a.settings.AssistantCodexPath())
	}
	return l.a.locator.Locate()
}
func (l providerLocator) Refresh() {
	if !l.a.usesChatGPT() {
		l.a.locator.Refresh()
	}
}
func (l providerLocator) SigningIn() bool {
	if l.a.usesChatGPT() {
		return l.a.chatGPT != nil && l.a.chatGPT.Connection().Status == chatgpt.SigningIn
	}
	return l.a.locator.SigningIn()
}
func (l providerLocator) SignedIn(done func(assistantpanel.SignIn)) {
	if l.a.usesChatGPT() {
		done(assistantpanel.SignIn{Known: true, SignedIn: l.a.chatGPTReady()})
		return
	}
	l.a.locator.SignedIn(done)
}
func (l providerLocator) OnSignInChange(f func()) func() {
	remove := l.a.OnChange(f)
	legacy := l.a.locator.OnSignInChange(f)
	return func() { remove(); legacy() }
}

// Board grants are separate from foreground panel/rewrite/search grants.
func (a *Assistant) boardProvider() assistantpanel.Provider {
	if !a.usesChatGPT() {
		return nil
	}
	if a.chatGPT == nil {
		return unavailableChatGPT{}
	}
	return chatgpt.NewProvider(chatgpt.Options{
		Executable: func() string { return chatgpt.Locate(a.settings.AssistantCodexPath()) },
		Bridge:     a.bridge, Socket: a.chatGPTSocket, Directory: filepath.Join(assistantDirectory(), "chatgpt"), Env: os.Environ(),
		Model:         a.settings.BoardChatGPTModel,
		HasConsent:    func() bool { return a.settings.BoardChatGPTConsentVersion() == 1 },
		AcceptConsent: func() { a.settings.SetBoardChatGPTConsentVersion(1) },
	}, a.chatGPT)
}

type boardProviderSettings struct{ providerSettings }

func (s boardProviderSettings) AssistantConsent() bool {
	if s.a.usesChatGPT() {
		return s.a.settings.BoardChatGPTConsentVersion() == 1
	}
	return s.providerSettings.AssistantConsent()
}
func (s boardProviderSettings) SetAssistantConsent(ok bool) {
	if !s.a.usesChatGPT() {
		s.providerSettings.SetAssistantConsent(ok)
	}
}

// reconnectChatGPT connects the ChatGPT account again (its sign-in in the
// browser, as Preferences → AI's Continue with ChatGPT) and calls done on
// the main loop with whether it worked; the board's triage then asks
// afresh whether its provider is signed in. The assistant panel's
// Reconnect to ChatGPT runs it (assistantpanel.Controller.ReconnectProvider);
// ctx is the panel's: Stop on "Connecting…" cancels it and with it the
// sign-in in the browser.
func (a *Assistant) reconnectChatGPT(ctx context.Context, done func(ok bool)) {
	if a.chatGPT == nil || a.chatGPTClosed {
		done(false)
		return
	}
	go func() {
		err := a.chatGPT.SignIn(ctx)
		glib.IdleAdd(func() {
			if a.chatGPTClosed {
				done(false)
				return
			}
			a.recheckBoardSignIn()
			done(err == nil)
		})
	}()
}

// recheckBoardSignIn asks the board's triage afresh whether its provider
// is signed in (after a ChatGPT sign-in elsewhere in the application).
func (a *Assistant) recheckBoardSignIn() {
	if a.board != nil {
		a.board.ctl.RecheckSignIn()
	}
}

// providerSwaps are the board's msgids that speak of Claude Code
// (board.ProviderSwappedTexts), read once; the ChatGPT provider says them
// in its own words (providerBoardTranslator).
var providerSwaps = board.ProviderSwappedTexts()

// Keep legacy Board presentation provider-aware without changing its pure model.
type providerBoardTranslator struct {
	a *Assistant
	board.Translator
}

func (t providerBoardTranslator) T(msgid string) string {
	if t.a != nil && t.a.usesChatGPT() {
		words := assistant.ChatGPTText(t.Translator)
		if swap, ok := providerSwaps[msgid]; ok {
			switch swap {
			case board.SwapNotFound:
				return words.NativeMissingCodex
			case board.SwapNotSignedIn:
				return words.Reconnect
			case board.SwapConsent:
				return words.BoardConsentBody
			}
		}
		// The assistant's own texts of Claude Code's sign-in that the
		// board's triage shows (assistant.SignInTexts, PanelTexts).
		switch msgid {
		case "Claude Code was not found on this computer":
			return words.NativeMissingCodex
		case "Claude Code is not signed in. Sign in under AI in the preferences.":
			return words.Reconnect
		case "Get Claude Code…":
			return words.Install
		case "Sign In…":
			return words.SignIn
		case "Waiting for the sign-in in your browser…":
			return words.Connecting
		}
	}
	return t.Translator.T(msgid)
}
