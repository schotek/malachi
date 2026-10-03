// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/htmlview"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The message pane: headers from the summary, then message.get and
// message.body in parallel; the loaded cache is shared with stand-alone
// message windows and the compose prefill.

// maxLoaded bounds the loaded cache by entries and maxLoadedBytes by the
// size of the bodies in it (an HTML body carries its inlined pictures);
// the oldest entries are evicted first.
const (
	maxLoaded      = 64
	maxLoadedBytes = 32 << 20
)

// bodySpinnerDelay is how long the body area stays blank before the
// spinner appears, in milliseconds. Bodies come from the local store, so
// most messages render sooner than this and never show a spinner at all;
// without the delay one would flash on every click, which is more
// distracting than the blank area it replaces.
const bodySpinnerDelay = 400

// loadedSeq numbers cache insertions so pruneLoaded can find the oldest.
// Touched on the main loop only.
var loadedSeq uint64

// loadedMessage is what fetchMessage produced for one message: the full
// header view, the body, or the error that prevented the body. A failed
// message.get is only logged (the summary headers stay) and retried the
// next time the message is shown; a failed body is retried the same way.
type loadedMessage struct {
	msg *api.Message
	// body is the body on display: without the quoted history (message.body
	// with trimQuoted), or whole while quotedShown.
	body *api.MessageBodyResult
	err  error // message.body failure

	// quotedShown: the quoted history is shown, body is the whole body,
	// asked for without trimQuoted (the view's Show Quoted Text,
	// conversation.QuotedReveal). Off by default: a body comes trimmed.
	quotedShown bool
	// otherBody is the other variant of the body, kept for switching back
	// without asking the daemon again; dropped when body is replaced under
	// another remote-content policy (the remote images, the pictures),
	// which it would not have. fetchingOther: a message.body for it is in
	// flight (a switch while the request for the variant shown before
	// ran). switchPolicy is the remote-content policy the variant switched
	// to is asked with when it has to be fetched (picturesPolicy of the
	// body shown before the switch: images the user loaded stay loaded).
	otherBody     *api.MessageBodyResult
	fetchingOther bool
	switchPolicy  api.RemoteContentPolicy

	seq uint64 // insertion order in Window.loaded

	// account is the account of the message, from the fetch that made the
	// entry: evictAccount finds the entries of an account whose messages
	// the daemon rebuilt (notify.messagesChanged). "" for an entry made
	// without one.
	account api.AccountID

	// In-flight halves and who wants to hear about them; a second
	// fetchMessage for the same id while one runs joins instead of asking
	// the daemon twice.
	getting, fetching bool
	waiters           []func(*loadedMessage)

	// loadingImages is set from the moment the user asks for the remote
	// images (Load Images, Always From This Sender) until the daemon has
	// answered; the bar shows it instead of the buttons (remote.go).
	loadingImages bool

	// loadingPictures is the same for the pictures kept on the mail server
	// only (Download Pictures): from the click until the message is
	// downloaded and its body asked for again (remote.go).
	loadingPictures bool

	// picturesRechecked is set once a picture of the body went missing and
	// the body was asked for again, until the next download of the message
	// (recheckPictures in remote.go): never more than once in between.
	picturesRechecked bool

	// unsubscribing is set from the click on the bulk strip's button until
	// message.unsubscribe has answered (bulk.go); the button waits.
	unsubscribing bool
}

// complete reports whether nothing is left to fetch.
func (lm *loadedMessage) complete() bool { return lm.msg != nil && lm.body != nil }

// bodySettled reports whether the body half has an answer (content or error).
func (lm *loadedMessage) bodySettled() bool { return lm.body != nil || lm.err != nil }

// size is what the entry costs the cache: its body, both variants of it.
func (lm *loadedMessage) size() int {
	n := 0
	for _, b := range []*api.MessageBodyResult{lm.body, lm.otherBody} {
		if b != nil {
			n += len(b.HTML) + len(b.Text)
		}
	}
	return n
}

// showQuoted shows the body with its quoted history (on) or without: the
// two variants trade places (body, otherBody, and their requests in
// flight), and a body error belongs to the variant left. true when
// anything changed; body is then nil when the variant has yet to be
// fetched. A port of the macOS client's LoadedMessage.showQuoted.
func (lm *loadedMessage) showQuoted(on bool) bool {
	if on == lm.quotedShown {
		return false
	}
	if lm.otherBody == nil {
		lm.switchPolicy = picturesPolicy(lm)
	}
	lm.quotedShown = on
	lm.body, lm.otherBody = lm.otherBody, lm.body
	lm.fetching, lm.fetchingOther = lm.fetchingOther, lm.fetching
	lm.err = nil
	return true
}

// store keeps a message.body answer asked for the variant quoted (with
// the quoted history or not): as body when that variant is still shown,
// else as otherBody. replacing (an answer under another remote-content
// policy) drops the other variant shown before.
func (lm *loadedMessage) store(res *api.MessageBodyResult, quoted, replacing bool) {
	if quoted != lm.quotedShown {
		lm.otherBody = res
		return
	}
	lm.body, lm.err = res, nil
	if replacing {
		lm.otherBody = nil
	}
}

// bodyAnswered ends the in-flight request for the variant quoted: the
// flag of the variant shown, or of the other one after a switch; whether
// it is the variant shown is returned.
func (lm *loadedMessage) bodyAnswered(quoted bool) bool {
	if quoted == lm.quotedShown {
		lm.fetching = false
		return true
	}
	lm.fetchingOther = false
	return false
}

// quotedOffer is the "•••" button under a body for what lm shows
// (conversation.OfferQuoted): Hide while the whole body shows (or is on
// its way, or failed: the way back to the trimmed one), Show when the
// daemon cut the quoted history from the body on display; none for nil.
func quotedOffer(lm *loadedMessage) conversation.QuotedOffer {
	if lm == nil {
		return conversation.QuotedNone
	}
	return conversation.OfferQuoted(lm.quotedShown, lm.err != nil, lm.body != nil && lm.body.QuotedTrimmed)
}

// bodyParams are the message.body parameters for the variant quoted of
// the body of message id: without trimQuoted only for the whole body.
func bodyParams(acc api.AccountID, id api.MessageID, quoted bool, policy api.RemoteContentPolicy) api.MessageBodyParams {
	return api.MessageBodyParams{AccountID: acc, MessageID: id, RemoteContent: policy, TrimQuoted: !quoted}
}

// messageView is one message display, shared in shape by the main pane and
// the stand-alone window: the header labels, the plain-text body, the HTML
// view (created when the first HTML message is shown) and the remote-image
// banner. Everything shown is server data: the labels never interpret
// markup, and the HTML view only ever gets the sanitiser's output.
type messageView struct {
	win    *Window
	parent *gtk.Window // for dialogs the view opens

	subject, date, body *gtk.Label
	hint                *gtk.Label // why only text is shown
	stack               *gtk.Stack // "text" | "loading" | "html"
	slot                *gtk.Box   // hosts html
	html                *htmlview.View

	// card is the issue card over the headers of a Jira message
	// (issue_card.go); nil on the view of an attached message, whose
	// builder has no slot for it.
	card *issueCard

	// The From, To and Cc chips (addresses.go).
	addresses *addressHeader

	// The attachment chips (attachments.go) and what is in the box now.
	attachments *adw.WrapBox
	chips       []gtk.Widgetter

	// What render was last given: the summary and the cache entry, for
	// redrawing the chips when the cache no longer holds the message
	// (refreshChips in download.go).
	shown       api.MessageSummary
	shownLoaded *loadedMessage

	// nested is set on the view of an attached message (embedded.go): its
	// parts have no numbers and message.part cannot serve them, so the
	// chips only name them.
	nested bool

	// The remote-image bar: the count, the buttons whose work the owner
	// supplies as load (this message) and trust (this sender; nil where
	// the bar has no such button), and the spinner that stands in for the
	// buttons while the images are on their way.
	bar                     *gtk.Box
	barLabel                *gtk.Label
	barSpinner              *adw.Spinner
	loadButton, trustButton *gtk.Button
	load, trust             func()

	// The bar of the pictures kept on the mail server only, built the same
	// way: the count, the button whose work the owner supplies as pictures,
	// and the spinner that stands in for it. picturesBar is nil on the view
	// of an attached message, whose pictures arrive inlined.
	picturesBar      *gtk.Box
	picturesLabel    *gtk.Label
	picturesSpinner  *adw.Spinner
	picturesDownload *gtk.Button
	pictures         func()

	// The bulk strip (bulk.go): the bar, its icon, text and button, whose
	// work the owner supplies as bulk. bulkBar is nil on the view of an
	// attached message, and on a conversation card until it needs one.
	bulkBar    *gtk.Box
	bulkIcon   *gtk.Image
	bulkLabel  *gtk.Label
	bulkAction *gtk.Button
	bulk       func()

	// toast shows a message in the owning window, when it wired one.
	toast func(string)

	// The "•••" under the body (quoted.go): its row, nil on the view of an
	// attached message and on a conversation card's share (the card has
	// its own); what it offers now; and the work its click does (Show
	// Quoted Text: on), which the owner supplies.
	quotedRow    gtk.Widgetter
	quotedButton *gtk.Button
	quotedNow    conversation.QuotedOffer
	onQuoted     func(on bool)

	links []api.Link // of the body on display, for link activation

	// spinner is the pending "reveal the spinner" timer, 0 when none.
	spinner glib.SourceHandle
}

// newMessageView binds the widgets of one message display from a builder;
// the object IDs are the same in window.blp, message_window.blp and
// embedded_window.blp, except pictures_bar and bulk_bar, which the last
// one lacks.
func newMessageView(w *Window, parent *gtk.Window, b *gtk.Builder) *messageView {
	v := &messageView{
		win:         w,
		parent:      parent,
		subject:     b.GetObject("message_subject").Cast().(*gtk.Label),
		date:        b.GetObject("message_date").Cast().(*gtk.Label),
		attachments: b.GetObject("message_attachments").Cast().(*adw.WrapBox),
		body:        b.GetObject("message_body").Cast().(*gtk.Label),
		hint:        b.GetObject("body_hint").Cast().(*gtk.Label),
		stack:       b.GetObject("body_stack").Cast().(*gtk.Stack),
		slot:        b.GetObject("html_slot").Cast().(*gtk.Box),
		bar:         b.GetObject("remote_bar").Cast().(*gtk.Box),
		barLabel:    b.GetObject("remote_label").Cast().(*gtk.Label),
		barSpinner:  b.GetObject("remote_spinner").Cast().(*adw.Spinner),
		loadButton:  b.GetObject("remote_load").Cast().(*gtk.Button),
		trustButton: b.GetObject("remote_trust").Cast().(*gtk.Button),
	}
	v.addresses = newAddressHeader(v, b)
	v.bindQuotedButton(b)
	if slot := b.GetObject("issue_card_slot"); slot != nil {
		v.card = newIssueCard(w, parent, func() (issueSubject, bool) { return subjectOf(v.shown) })
		box := slot.Cast().(*gtk.Box)
		box.Append(v.card)
		// The slot shows with its card: an empty box would still count in
		// the headers' spacing and push the subject of a mail message down.
		box.SetVisible(false)
		v.card.NotifyProperty("visible", func() { box.SetVisible(v.card.Visible()) })
	}
	v.plain()
	v.hint.SetLabel(i18n.T("The formatted version of this message could not be shown safely; this is its plain text."))
	load, trust := v.loadButton, v.trustButton
	load.ConnectClicked(func() {
		if v.load != nil {
			v.load()
		}
	})
	trust.ConnectClicked(func() {
		if v.trust != nil {
			v.trust()
		}
	})
	// The bar goes away the moment the images are in, and hiding the widget
	// that holds the focus makes GTK pass the focus to the next one in the
	// chain — one of the selectable header labels, which selects all of its
	// text as soon as it is focused (see the note on focus-widget in
	// message_window.blp). The message then looked as though it had selected
	// itself while the user was still letting go of the button. Clicking
	// these two therefore leaves the focus where it was; setBarVisible takes
	// care of the keyboard, which has to focus the button to press it.
	load.SetFocusOnClick(false)
	trust.SetFocusOnClick(false)
	if bar := b.GetObject("bulk_bar"); bar != nil {
		v.bulkBar = bar.Cast().(*gtk.Box)
		v.bulkIcon = b.GetObject("bulk_icon").Cast().(*gtk.Image)
		v.bulkLabel = b.GetObject("bulk_label").Cast().(*gtk.Label)
		v.bulkAction = b.GetObject("bulk_action").Cast().(*gtk.Button)
		v.bindBulk()
		v.bulk = func() { w.unsubscribe(v.parent, v.shown.ID, v.bulkSay) }
	}
	if bar := b.GetObject("pictures_bar"); bar != nil {
		v.picturesBar = bar.Cast().(*gtk.Box)
		v.picturesLabel = b.GetObject("pictures_label").Cast().(*gtk.Label)
		v.picturesSpinner = b.GetObject("pictures_spinner").Cast().(*adw.Spinner)
		v.picturesDownload = b.GetObject("pictures_download").Cast().(*gtk.Button)
		v.picturesLabel.SetUseMarkup(false)
		v.picturesDownload.ConnectClicked(func() {
			if v.pictures != nil {
				v.pictures()
			}
		})
		// Like the buttons above: the bar goes away once the pictures are in.
		v.picturesDownload.SetFocusOnClick(false)
	}
	return v
}

// setBarVisible shows or hides the remote-image bar, taking the focus out of
// it first: the body is somewhere harmless to put it, unlike the selectable
// label GTK would pick (see newMessageView).
func (v *messageView) setBarVisible(show bool) {
	if !show && v.bar.FocusChild() != nil {
		v.stack.GrabFocus()
	}
	v.bar.SetVisible(show)
}

// setBarLoading switches the bar between offering the images and showing
// that they are on their way: the buttons make way for the spinner, so a
// click has a visible answer even when the daemon takes its time on a slow
// connection. Hiding a focused button would move the focus to the
// selectable labels (see newMessageView), so it leaves the bar first.
func (v *messageView) setBarLoading(loading bool) {
	if loading && v.bar.FocusChild() != nil {
		v.stack.GrabFocus()
	}
	v.barSpinner.SetVisible(loading)
	v.loadButton.SetVisible(!loading)
	v.trustButton.SetVisible(!loading && v.trust != nil)
}

// paneLabels is the main window's message pane.
func (w *Window) paneLabels() *messageView { return w.pane }

// plain switches markup off on every label (CLAUDE.md rule 3).
func (v *messageView) plain() {
	for _, lb := range []*gtk.Label{v.subject, v.date, v.body, v.hint, v.barLabel} {
		lb.SetUseMarkup(false)
	}
}

// htmlView is the WebKit view, created on first use: a plain-text mailbox
// never starts a web process.
func (v *messageView) htmlView() *htmlview.View {
	if v.html == nil {
		v.html = htmlview.New(v.win.log, v.win.fetchPart)
		v.html.OnLink = func(uri string) { v.win.openLink(v.parent, uri, v.links) }
		v.html.SetZoom(v.win.settings.TextZoom())
		v.slot.Append(v.html)
	}
	return v.html
}

// close ends the HTML view's web process, if there is one: the view's
// window closes.
func (v *messageView) close() {
	v.cancelSpinner()
	if v.html != nil {
		v.html.Close()
	}
}

// setZoom pushes the text-zoom setting to the HTML view, if there is one.
func (v *messageView) setZoom(percent int) {
	if v.html != nil {
		v.html.SetZoom(percent)
	}
}

// showText shows plain text in the body area.
func (v *messageView) showText(text string) {
	v.body.SetLabel(text)
	v.stack.SetVisibleChildName("text")
	if v.html != nil {
		v.html.Clear() // drop the pictures of the previous message
	}
}

// renderHeaders shows the headers: from the summary alone, or from the full
// message when m is not nil (recipients with Cc). The attachment chips are
// renderAttachments' business: they depend on the body too. A message of a
// Jira account gets the issue card, and the issue's summary is its subject
// (the card has the key); what it shows of the issue is returned (nil for
// mail).
func (v *messageView) renderHeaders(s api.MessageSummary, m *api.Message) *issueReading {
	from, to, date := s.From, s.To, s.Date
	var cc []api.Address
	summary := s
	if m != nil {
		from, to, cc, date = m.From, m.To, m.CC, m.Date
		s.Subject = m.Subject
	}
	var issue *issueReading
	if r, ok := readIssue(summary, m, v.win.model.issueSite(s.AccountID)); ok {
		issue = &r
		v.subject.SetLabel(r.subject)
	} else {
		v.subject.SetLabel(subjectText(s.Subject))
	}
	if v.card != nil {
		v.card.show(issue, s.AccountID, v.win.issues.canTransition(s.AccountID))
	}
	v.addresses.show(s.ID, s.AccountID, from, to, cc)
	if date.IsZero() {
		v.date.SetLabel("")
	} else {
		v.date.SetLabel(widget.FormatDateTime(date))
	}
	return issue
}

// renderEvent shows an event of an issue (a status or assignee change) in
// place of a body: its changes as sentences (issueReading.eventBody), no
// bars.
func (v *messageView) renderEvent(text string) {
	v.cancelSpinner()
	v.showQuotedButton(conversation.QuotedNone)
	v.links = nil
	v.hint.SetVisible(false)
	v.setBarVisible(false)
	v.showPicturesBar(picturesBarState{})
	v.showText(text)
}

// applyIssue shows the refreshed issue of a transition (issueActions
// onIssue) on the card at once when it is the issue on display; the
// message's own summary follows with the daemon's notifications.
func (v *messageView) applyIssue(acc api.AccountID, info api.IssueInfo) {
	s := v.shown
	if v.card == nil || s.AccountID != acc || s.Issue == nil || jira.Clean(s.Issue.Key) != jira.Clean(info.Key) {
		return
	}
	item := *s.Issue
	item.IssueInfo = info
	s.Issue = &item
	r, ok := readIssue(s, nil, v.win.model.issueSite(acc))
	if !ok {
		return
	}
	v.card.show(&r, acc, v.win.issues.canTransition(acc))
	v.subject.SetLabel(r.subject)
}

// renderBody shows the body, its state, or the error that prevented it:
// the sanitised HTML in the web view when there is one, the plain text
// otherwise, with a hint when the HTML was withheld, the banner when
// remote images were removed and the one for pictures kept on the mail
// server only.
func (v *messageView) renderBody(lm *loadedMessage) {
	v.cancelSpinner()
	b, err := lm.body, lm.err
	v.links = nil
	if err != nil {
		v.hint.SetVisible(false)
		v.setBarVisible(false)
		v.showPicturesBar(picturesBarState{})
		v.showText(widget.RPCErrorText(i18n.T("Loading the message"), err))
		return
	}
	if showsHTML(b) {
		v.links = b.Links
		v.hint.SetVisible(false)
		v.htmlView().Load(b.HTML)
		v.stack.SetVisibleChildName("html")
		renderRemoteBar(v, lm)
		renderPicturesBar(v, lm)
		return
	}
	v.hint.SetVisible(b != nil && b.HTMLWithheld)
	v.setBarVisible(false)
	v.showPicturesBar(picturesBarState{})
	v.showText(bodyText(b))
}

// render shows whatever lm holds so far: full headers once message.get
// answered, the body once message.body did, the attachment chips from
// both, and the "•••" under a body whose quoted history was cut (or
// shows). A nil lm shows the summary and the loading placeholder.
func (v *messageView) render(s api.MessageSummary, lm *loadedMessage) {
	v.shown, v.shownLoaded = s, lm
	v.renderBulk(s, lm)
	if lm == nil {
		if issue := v.renderHeaders(s, nil); issue != nil && issue.event {
			v.renderEvent(issue.eventBody)
		} else {
			v.loading()
		}
		v.renderAttachments(s, nil)
		v.showQuotedButton(conversation.QuotedNone)
		return
	}
	issue := v.renderHeaders(s, lm.msg)
	if issue != nil && issue.event {
		v.renderEvent(issue.eventBody)
	} else {
		if lm.bodySettled() {
			v.renderBody(lm)
		} else {
			v.loading()
		}
		v.showQuotedButton(quotedOffer(lm))
	}
	v.renderAttachments(s, lm)
}

// loading empties the body area while the body is on its way and, if it
// takes long enough to notice, shows a spinner in the middle of it.
func (v *messageView) loading() {
	v.cancelSpinner()
	v.links = nil
	v.hint.SetVisible(false)
	v.setBarVisible(false)
	v.showPicturesBar(picturesBarState{})
	// Blank at once: this also drops the pictures of the message before.
	v.showText("")
	v.spinner = glib.TimeoutAdd(bodySpinnerDelay, func() bool {
		v.spinner = 0
		v.stack.SetVisibleChildName("loading")
		return false
	})
}

// cancelSpinner disarms the pending spinner, if any. Safe to call twice
// and after the view is gone.
func (v *messageView) cancelSpinner() {
	if v.spinner != 0 {
		glib.SourceRemove(v.spinner)
		v.spinner = 0
	}
}

// subjectText is the subject to display; an empty one gets a placeholder.
func subjectText(subject string) string {
	if s := strings.TrimSpace(subject); s != "" {
		return s
	}
	return i18n.T("(No subject)")
}

// bodyText is what the body label shows for a message.body result.
func bodyText(b *api.MessageBodyResult) string {
	if b == nil {
		return i18n.T("(Empty message)")
	}
	switch b.BodyState {
	case api.BodyPending:
		return i18n.T("Downloading…")
	case api.BodyTooBig:
		return i18n.T("This message is too large to download.")
	case api.BodyFailed:
		return i18n.T("This message could not be read.")
	}
	text := strings.TrimRight(b.Text, " \t\r\n")
	if strings.TrimSpace(text) == "" {
		return i18n.T("(Empty message)")
	}
	return text
}

// showMessage displays message id in the pane: summary headers at once,
// the rest (and the outbox banner, for a queued message) when fetchMessage
// returns.
func (w *Window) showMessage(id api.MessageID) {
	s, _, ok := w.model.message(id)
	if !ok {
		return
	}
	gen := w.model.bumpBody()
	// What the user revealed of another message is forgotten.
	w.paneQuoted.Show(string(id))
	labels := w.paneLabels()
	labels.render(s, nil)
	w.outboxBanner.SetRevealed(false)
	w.draftBanner.SetRevealed(w.model.inDrafts(s))
	w.messageStack.SetVisibleChildName("message")
	if readsWithoutBody(s) {
		// A status or assignee change: its changes are the whole message,
		// there is no body to fetch.
		return
	}
	w.fetchMessageQuoted(s.AccountID, id, w.paneQuoted.IsRevealed(id), func(lm *loadedMessage) {
		if gen != w.model.bodyGen {
			return // the pane moved on
		}
		labels.render(s, lm)
		renderOutboxBanner(w.outboxBanner, lm.msg)
	})
}

// fetchMessage runs message.get and message.body for id (each only when
// the cache lacks it and no request is in flight) and calls done on the
// main loop after every answer, with the cache entry so far; a complete
// entry calls done at once. Callers guard staleness themselves (the pane by
// bodyGen, a message window by its closed flag): the cache is keyed by id
// and stays valid whatever the pane shows now.
func (w *Window) fetchMessage(acc api.AccountID, id api.MessageID, done func(*loadedMessage)) {
	lm := w.loadedFor(id)
	if lm.account == "" {
		lm.account = acc
	}
	if lm.complete() {
		done(lm)
		return
	}
	lm.waiters = append(lm.waiters, done)
	if lm.msg == nil && !lm.getting {
		lm.getting = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			defer cancel()
			var res api.MessageGetResult
			err := w.client.Call(ctx, api.MethodMessageGet, api.MessageGetParams{AccountID: acc, MessageID: id}, &res)
			glib.IdleAdd(func() {
				lm.getting = false
				if err != nil {
					w.log.Warn("message.get", "err", err)
				} else {
					lm.msg = &res.Message
				}
				w.settleLoaded(id, lm)
			})
		}()
	}
	if lm.body == nil && !lm.fetching {
		w.startBody(acc, id, lm)
	}
}

// startBody runs message.body for id into lm (the body half of
// fetchMessage and fetchBodyOnly), for the variant of the body the entry
// shows (quotedShown). An answer that arrives after a view switched to the
// other variant is kept aside for switching back (loadedMessage.store);
// its failure is only logged.
func (w *Window) startBody(acc api.AccountID, id api.MessageID, lm *loadedMessage) {
	quoted := lm.quotedShown
	params := bodyParams(acc, id, quoted, lm.switchPolicy)
	lm.fetching = true
	lm.err = nil // a retry after a failure
	go func() {
		// The stored policy may let the daemon fetch remote images first.
		ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
		defer cancel()
		var res api.MessageBodyResult
		err := w.client.Call(ctx, api.MethodMessageBody, params, &res)
		glib.IdleAdd(func() {
			current := lm.bodyAnswered(quoted)
			if err != nil {
				w.log.Warn("message.body", "err", err)
				if current {
					lm.err = err
				}
			} else {
				lm.store(&res, quoted, false)
			}
			w.settleLoaded(id, lm)
		})
	}()
}

// switchQuoted shows the variant quoted of the body of message id (with
// its quoted history or without, loadedMessage.showQuoted): a variant held
// already shows at once wherever the message is on display (showLoaded),
// one to fetch is the caller's next step (fetchMessage, fetchBodyOnly).
// The entry grew by the variant it keeps aside, so the cache is pruned.
func (w *Window) switchQuoted(id api.MessageID, lm *loadedMessage, quoted bool) {
	if !lm.showQuoted(quoted) {
		return
	}
	pruneLoaded(w.loaded, maxLoaded, maxLoadedBytes)
	if lm.body != nil {
		w.showLoaded(id, lm)
	}
}

// fetchMessageQuoted is fetchMessage for the variant quoted of the body
// (switchQuoted first): the view that shows the message names the variant
// the user chose for it there.
func (w *Window) fetchMessageQuoted(acc api.AccountID, id api.MessageID, quoted bool, done func(*loadedMessage)) {
	lm := w.loadedFor(id)
	if lm.account == "" {
		lm.account = acc
	}
	w.switchQuoted(id, lm, quoted)
	w.fetchMessage(acc, id, done)
}

// settleLoaded runs on the main loop after one half of lm arrived: the
// entry is put back if it was evicted meanwhile and the waiters hear about
// it; once nothing is in flight any more they are dropped.
func (w *Window) settleLoaded(id api.MessageID, lm *loadedMessage) {
	if w.loaded[id] == nil {
		w.storeLoaded(id, lm)
	}
	waiters := lm.waiters
	if !lm.getting && !lm.fetching {
		lm.waiters = nil
	}
	for _, done := range waiters {
		done(lm)
	}
}

// loadedFor is the cache entry of id, created empty when there is none.
func (w *Window) loadedFor(id api.MessageID) *loadedMessage {
	lm := w.loaded[id]
	if lm == nil {
		lm = &loadedMessage{}
		w.storeLoaded(id, lm)
	}
	return lm
}

// storeLoaded caches lm for id, evicting the oldest entries beyond the
// caps.
func (w *Window) storeLoaded(id api.MessageID, lm *loadedMessage) {
	loadedSeq++
	lm.seq = loadedSeq
	w.loaded[id] = lm
	pruneLoaded(w.loaded, maxLoaded, maxLoadedBytes)
}

// pruneLoaded evicts the entries with the lowest seq until at most limit
// remain and their bodies fit in maxBytes; the newest entry always stays.
func pruneLoaded(m map[api.MessageID]*loadedMessage, limit, maxBytes int) {
	total := 0
	for _, lm := range m {
		total += lm.size()
	}
	for len(m) > 1 && (len(m) > limit || total > maxBytes) {
		var oldest api.MessageID
		first := true
		for id, lm := range m {
			if first || lm.seq < m[oldest].seq {
				oldest, first = id, false
			}
		}
		total -= m[oldest].size()
		delete(m, oldest)
	}
}

// summary is what the window knows about message id: the list entry, or
// the cached full message when the list has moved on (a message window
// outliving the folder it was opened from), or the user's reply in Sent
// that a conversation shows (mailModel.sentMessage).
func (w *Window) summary(id api.MessageID) (api.MessageSummary, bool) {
	if s, _, ok := w.model.message(id); ok {
		return s, true
	}
	if lm := w.loaded[id]; lm != nil && lm.msg != nil {
		return lm.msg.MessageSummary, true
	}
	// The user's reply in Sent a conversation shows (its card's reply and
	// forward, its images).
	return w.model.sentMessage(id)
}

// openMessage opens message s as a double click does, on its row in the
// list or on the header of its card in a conversation: a message of a
// Drafts folder in the compose window, any other in a window of its own.
func (w *Window) openMessage(s api.MessageSummary) {
	if w.model.inDrafts(s) {
		w.openDraft(s.ID)
		return
	}
	w.openMessageWindow(s.ID)
}

// openMessageWindow opens message id in its own window, or raises the
// window that already shows it (w.openMessages).
func (w *Window) openMessageWindow(id api.MessageID) {
	if mw, ok := w.openMessages[id]; ok {
		mw.Present()
		return
	}
	s, ok := w.summary(id)
	if !ok {
		return
	}
	mw := newMessageWindow(w, s)
	w.openMessages[id] = mw
	mw.ConnectCloseRequest(func() bool {
		mw.closed = true
		// The only timer that would outlive the window, and the web
		// process, which would live until the view is collected.
		mw.view.close()
		delete(w.openMessages, id)
		return false
	})
	mw.Present()
	if readsWithoutBody(s) {
		return // an event of an issue: nothing to fetch
	}
	w.fetchMessageQuoted(s.AccountID, id, mw.quoted.IsRevealed(id), func(lm *loadedMessage) {
		if mw.closed {
			return
		}
		mw.show(s, lm)
	})
}

// closeMessageWindow closes the stand-alone window of message id, if any,
// and the windows of the messages attached to it (the message left the
// folder).
func (w *Window) closeMessageWindow(id api.MessageID) {
	if mw, ok := w.openMessages[id]; ok {
		mw.Close()
	}
	w.closeEmbeddedWindows(id)
}
