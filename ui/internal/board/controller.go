// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"reflect"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The board, the controller half: what the user chose to look at (the
// style, the filters, the selection, the "Why is this here?" box) and the
// view model built from it and the source's snapshot (View). No widgets:
// the board page and its styles lay the view model out, ask for changes
// here and redraw what OnChange names. What the user decides about a case
// (its state, done, a remind, archive, a discarded draft, a promise ticked
// off) goes to the source, which keeps it; the view model follows once the
// source reports it. Selecting a case asks the source for its conversation
// (DataSource.LoadMessages); the detail says it loads until it arrives. The
// source's toasts (a refused write, what Archive did) reach the page
// through OnToast.
//
// The macOS client leads (MalachiCore/Controllers/BoardController.swift);
// this is its port. Every method runs on the main loop, and so do the
// callbacks.

// Changes says what changed, for the page; several at once are possible.
type Changes uint8

// The changes.
const (
	// ChangeContent: the cases shown changed (rows, sections, columns, the
	// Today page, the counts, or the selected case's detail itself).
	ChangeContent Changes = 1 << iota
	// ChangeSelection: another case is selected, none is, the panel opens
	// or closes, or the "Why is this here?" box opens or closes.
	ChangeSelection
	// ChangeStyle: State().Style changed.
	ChangeStyle
	// ChangeFilters: State().Filter or State().Account changed.
	ChangeFilters
)

// Has reports whether c holds all of x.
func (c Changes) Has(x Changes) bool { return c&x == x }

// ControllerOptions are what a controller runs with besides its source.
type ControllerOptions struct {
	// Env writes the view model (the translator, the dates, the zone).
	Env Env
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// DefaultStyle is the nick of Board View (the key
	// board-default-style), asked for each time the board shows until the
	// user picks a style (StyleOnShow); nil is Last Used.
	DefaultStyle func() string
	// LastStyle is the nick of the style used last (the key
	// board-last-style, which the page writes on every ChangeStyle); nil
	// is the List.
	LastStyle func() string
	// SavedAccount is the account filter saved (the key
	// board-account-filter, which the page writes on every ChangeFilters),
	// asked for the first time the board shows; nil is every account.
	SavedAccount func() string
}

// Controller is the board's view state and view model (BoardController).
// One observer, OnChange: the board page, which fans the changes out to
// its children.
type Controller struct {
	// OnChange is called after every change of State or View, with what
	// changed.
	OnChange func(Changes)
	// OnToast is called with a short sentence for a toast: a write the
	// source could not make (undone by then), or what Archive did.
	OnToast func(string)
	// OnArchived is called with what Archive did, for a toast with Undo
	// (ArchiveOutcome.Text and Undo; UndoArchive takes it back); nil
	// shows the text through OnToast.
	OnArchived func(ArchiveOutcome)

	source       DataSource
	env          Env
	now          func() time.Time
	defaultStyle func() string
	lastStyle    func() string
	savedAccount func() string
	// picked: the user chose a style in this run (SetStyle,
	// ShowWaitingForYou); the board then keeps it (StyleOnShow).
	picked bool
	// userPicked is the case the user selected explicitly (Select with a
	// case), not the List's automatic first row; paneLive says whether a
	// reply pane is live for a case (SetPaneLive).
	userPicked CaseID
	paneLive   func(CaseID) bool
	// pendingAccount is the saved account filter waiting for the accounts
	// to be known (FilterOnShow); "" when none waits.
	pendingAccount string
	state          ViewState
	view           ViewModel
	hasShown       bool
	// departure is where the selection goes when the selected case leaves
	// what is shown after the user's own write (done, reopened, moved out
	// of the filter): computed before the write, used by the first report
	// of the source that follows it (which a source may send later) and
	// then dropped, whatever that report holds. A write that changes
	// nothing notes none, and any view state the user asks for meanwhile
	// drops it.
	departure *departure
	// pending and notifying: changes not yet delivered to OnChange, and
	// whether a delivery is under way; a listener that calls the
	// controller from OnChange gets that change after its own call
	// returns, not inside it.
	pending   Changes
	notifying bool
	// requested is the case and version whose conversation was last asked
	// for, and the phase then: asked again when another case is selected
	// or the case changed, not on every report. A failed load is asked
	// again when the user selects the case again or asks to
	// (RetryMessages), and when the board came back from a failure (a
	// reconnect).
	requested *requested
}

type departure struct {
	id, next CaseID
}

type requested struct {
	id      CaseID
	version int64
	phase   Phase
}

// NewController builds the view model of src and installs itself as src's
// observer. Until the board shows it holds the List (BoardWillShow). It
// reports nothing while it is made, but may ask src for the selected
// case's conversation.
func NewController(src DataSource, o ControllerOptions) *Controller {
	c := &Controller{source: src, env: o.Env, now: o.Now, defaultStyle: o.DefaultStyle, lastStyle: o.LastStyle,
		savedAccount: o.SavedAccount}
	if c.now == nil {
		c.now = time.Now
	}
	st := NewViewState()
	st.Selection = ResolveSelection(src.Snapshot(), st)
	c.state = st
	c.view = View(src.Snapshot(), st, c.now(), c.env)
	src.SetHandlers(Handlers{
		Change: c.Refresh,
		Error:  c.toast,
		Notice: c.toast,
		Archived: func(o ArchiveOutcome) {
			if c.OnArchived != nil {
				c.OnArchived(o)
				return
			}
			c.toast(o.Text)
		},
	})
	c.requestMessages()
	return c
}

func (c *Controller) toast(text string) {
	if c.OnToast != nil {
		c.OnToast(text)
	}
}

// Source is the controller's source.
func (c *Controller) Source() DataSource { return c.source }

// State is what the user looks at; its Selection is always the resolved
// one (View().Selection).
func (c *Controller) State() ViewState { return c.state }

// View is the view model.
func (c *Controller) View() ViewModel { return c.view }

// Phase is how far the source's data is (View().Phase once built).
func (c *Controller) Phase() Phase { return c.source.Snapshot().Phase }

// HasShown reports whether the board has shown in this run
// (BoardWillShow).
func (c *Controller) HasShown() bool { return c.hasShown }

// RemindPresets are the remind presets for now.
func (c *Controller) RemindPresets() []RemindChoice { return RemindPresets(c.now(), c.env) }

// What the user looks at.

// SetPaneLive installs the question "is a reply pane live for this case"
// (an inline editor with text or a save in flight), which decides whether a
// selection survives a style switch or a narrowing (keepsSelection).
func (c *Controller) SetPaneLive(f func(CaseID) bool) { c.paneLive = f }

// keepsSelection is the rule of SetStyle and SetInlineDetail(false), for
// the Swift and C# ports to mirror: the selection is kept only when a reply
// pane is live for the case or when the user selected that case explicitly
// (Select). The List's automatic first row is not kept: Columns and Today
// would otherwise slide their panel in for a case nobody chose, and the
// overview page opens without a panel.
func (c *Controller) keepsSelection() bool {
	id := c.state.Selection
	if id == "" {
		return false
	}
	return id == c.userPicked || (c.paneLive != nil && c.paneLive(id))
}

// SetStyle is the user's switch of the style. The selected case stays
// selected only when keepsSelection (a live reply pane, or an explicit
// pick; Columns and Today show it in their panel, so an inline reply
// editor moves there); else the selection is cleared: Columns and Today
// show no panel and the list selects its first row when its detail is
// beside it. The board keeps the style from now on in this run
// (StyleOnShow).
func (c *Controller) SetStyle(s Style) {
	c.picked = true
	c.setStyle(s)
}

func (c *Controller) setStyle(s Style) {
	if s == c.state.Style {
		return
	}
	next := c.state
	next.Style = s
	if !c.keepsSelection() {
		next.Selection = ""
	}
	c.apply(next, true)
}

// SetFilter switches the list's filter and clears the selection (the list
// then selects its first row).
func (c *Controller) SetFilter(f Filter) {
	if f.Same(c.state.Filter) {
		return
	}
	next := c.state
	next.Filter = f
	next.Selection = ""
	c.apply(next, true)
}

// SetAccount switches the account filter ("" = every account) and clears
// the selection (the list then selects its first row).
func (c *Controller) SetAccount(a api.AccountID) {
	c.pendingAccount = ""
	if a == c.state.Account {
		return
	}
	next := c.state
	next.Account = a
	next.Selection = ""
	c.apply(next, true)
}

// Select selects id, or nothing (""). A case that is not shown selects
// nothing; in the list with its detail beside it, nothing is its first
// row.
func (c *Controller) Select(id CaseID) {
	if id != "" && id == c.state.Selection && c.failedLoad(id) {
		// Selected again: its conversation is asked for again.
		c.requested = nil
	}
	next := c.state
	next.Selection = id
	c.userPicked = id
	c.apply(next, true)
}

// SetInlineDetail says whether the list has room for the detail beside
// it. Folding the detail away keeps the selection only when keepsSelection
// (a live reply pane or an explicit pick): that case's detail (and an
// inline reply editor in it) moves to the panel, otherwise nothing is
// selected and no panel slides in; unfolding shows it beside the list
// again, or selects the first row.
func (c *Controller) SetInlineDetail(on bool) {
	if on == c.state.InlineDetail {
		return
	}
	next := c.state
	next.InlineDetail = on
	if !on && !c.keepsSelection() {
		next.Selection = ""
	}
	c.apply(next, true)
}

// ToggleWhy opens or closes the detail's "Why is this here?" box. Nothing
// without a selection.
func (c *Controller) ToggleWhy() {
	if c.state.Selection == "" {
		return
	}
	next := c.state
	next.RevealsWhy = !next.RevealsWhy
	c.apply(next, true)
}

// ShowWaitingForYou shows the list filtered to the cases waiting for the
// user (the Today page's "and N more").
func (c *Controller) ShowWaitingForYou() {
	c.picked = true
	next := c.state
	next.Style = StyleList
	next.Filter = Filter{Kind: FilterState, State: StateYou}
	if next.Style != c.state.Style || !next.Filter.Same(c.state.Filter) {
		next.Selection = ""
	}
	c.apply(next, true)
}

// Refresh builds the view model anew: after the source changed, or when
// the date may have (a new day moves the deadlines).
func (c *Controller) Refresh() {
	c.apply(c.state, false)
}

// BoardWillShow is called as the board is about to show (the window
// enters Board mode, before its page is laid out): until the user picks a
// style it takes Board View (StyleOnShow), and the first time in a run the
// saved account filter (FilterOnShow; once the accounts are known).
func (c *Controller) BoardWillShow() {
	first := !c.hasShown
	c.hasShown = true
	if first && c.savedAccount != nil {
		c.pendingAccount = c.savedAccount()
	}
	def := DefaultStyle{Last: true}
	if c.defaultStyle != nil {
		def = ParseDefaultStyle(c.defaultStyle())
	}
	last := StyleList
	if c.lastStyle != nil {
		last = ParseStyle(c.lastStyle())
	}
	next := c.state
	next.Style = StyleOnShow(def, last, c.state.Style, c.picked)
	if next.Style != c.state.Style || c.pendingAccount != "" {
		c.apply(next, true)
	}
}

// UndoArchive takes back what Archive did (the toast's Undo): the
// messages go back to their folders and the case back on the board
// (UndoArchive's calls, through a source that can move messages; any other
// source only reopens the case).
func (c *Controller) UndoArchive(o ArchiveOutcome) {
	c.departure = nil
	if u, ok := c.source.(ArchiveUndoer); ok {
		u.UndoArchive(o)
		return
	}
	c.source.SetDone(o.Case, false)
}

// BoardShown is called once the board shows again (the window entered
// Board mode): a board that could not be listed is asked for again at
// once, then the view model is built anew (Refresh).
func (c *Controller) BoardShown() {
	if c.Phase().IsFailure() {
		c.source.Refresh()
	}
	c.Refresh()
}

// RetryMessages is the detail's Try Again: it asks for the selected case's
// conversation again after it could not be loaded.
func (c *Controller) RetryMessages() {
	if !c.failedLoad(c.state.Selection) {
		return
	}
	c.requested = nil
	c.requestMessages()
}

// What the user decides about a case.

// SetState moves the case to s. Moving it to the state it would have by
// itself (the assistant's, or the rules') puts it back to automatic.
func (c *Controller) SetState(id CaseID, s State) {
	snapshot := c.source.Snapshot()
	k, ok := snapshot.Case(id)
	if !ok {
		return
	}
	current := k.UserState
	k.UserState = nil
	var value *State
	if s != StateOf(k, snapshot.Annotated) {
		value = statePtr(s)
	}
	changes := (value == nil) != (current == nil) || (value != nil && *value != *current)
	c.write(id, changes, func() { c.source.SetState(id, value) })
}

// MarkDone marks the case done. The list selects the next row, else the
// previous one; Columns and Today select nothing.
func (c *Controller) MarkDone(id CaseID) {
	k, ok := c.source.Snapshot().Case(id)
	c.write(id, ok && !k.Done(), func() { c.source.SetDone(id, true) })
}

// Reopen puts a done case back on the board.
func (c *Controller) Reopen(id CaseID) {
	k, ok := c.source.Snapshot().Case(id)
	c.write(id, ok && k.Done(), func() { c.source.SetDone(id, false) })
}

// Remind hides the case until *until; nil puts a snoozed case back on the
// board. The selection moves on as after done.
func (c *Controller) Remind(id CaseID, until *time.Time) {
	k, ok := c.source.Snapshot().Case(id)
	changes := false
	if ok {
		if until != nil {
			changes = !k.Visibility.Equal(Visibility{Kind: VisibleSnoozed, At: *until})
		} else {
			_, changes = k.Visibility.RemindAt()
		}
	}
	c.write(id, changes, func() { c.source.Remind(id, until) })
}

// Archive archives the case (where its account can) and marks it done; the
// toast says what it did. The selection moves on as after done.
func (c *Controller) Archive(id CaseID) {
	k, ok := c.source.Snapshot().Case(id)
	c.write(id, ok && !k.Visibility.IsDone(), func() { c.source.Archive(id) })
}

// SetCommitmentDone ticks a promise off, or reopens it.
func (c *Controller) SetCommitmentDone(id api.BoardCommitmentID, done bool) {
	c.departure = nil
	c.source.SetCommitmentDone(id, done)
}

// DiscardDraft drops the assistant's suggested reply.
func (c *Controller) DiscardDraft(id CaseID) {
	c.source.DiscardDraft(id)
}

// DiscardStoredDraft is the inline reply editor's Discard: it deletes
// draft (the one the editor edits, whatever the case links by now) and
// calls done when that is done, with the error when it was refused
// (DataSource.DiscardStoredDraft).
func (c *Controller) DiscardStoredDraft(id CaseID, draft api.DraftID, account api.AccountID, done func(error)) {
	c.source.DiscardStoredDraft(id, draft, account, done)
}

// Unflag is Unstar: it removes the star that keeps the case hot. The rules
// then decide where the case goes, so no departure is noted: the selection
// stays while the case is on the board.
func (c *Controller) Unflag(id CaseID) {
	c.source.Unflag(id)
}

// Internals.

// write runs the user's write on case id; when id is selected and the
// write changes the case, it notes where the selection goes should the
// case leave what is shown. A write that changes nothing drops a departure
// noted before: no report of the source belongs to it.
func (c *Controller) write(id CaseID, changes bool, body func()) {
	if changes && c.state.Selection == id {
		c.departure = &departure{id: id, next: SelectionAfterDone(id, c.source.Snapshot(), c.state)}
	} else {
		c.departure = nil
	}
	body()
}

// apply makes next the state, its selection resolved, rebuilds the view
// model and tells the page what changed. user: the user asked for another
// view state (a pending departure no longer applies).
func (c *Controller) apply(next ViewState, user bool) {
	snapshot := c.source.Snapshot()
	if c.pendingAccount != "" && len(snapshot.Accounts) > 0 {
		// The saved filter, once the accounts are known; an account that
		// went away leaves every account.
		if a := api.AccountID(FilterOnShow(c.pendingAccount, snapshot.Accounts)); a != next.Account {
			next.Account = a
			next.Selection = ""
		}
		c.pendingAccount = ""
	}
	if next.Account != "" && !hasAccount(snapshot, next.Account) {
		// The account went away: its filter with it.
		next.Account = ""
	}
	// Only the first report after the user's write may use the departure;
	// a later, unrelated one never does.
	var d *departure
	if !user {
		d = c.departure
	}
	c.departure = nil
	resolved := ResolveSelection(snapshot, next)
	if d != nil && next.Selection == d.id && resolved != d.id {
		// The selected case left after the user's write.
		next.Selection = d.next
		resolved = ResolveSelection(snapshot, next)
	}
	next.Selection = resolved
	if next.Selection != c.state.Selection {
		next.RevealsWhy = false
	}
	view := View(snapshot, next, c.now(), c.env)

	var changes Changes
	if next.Style != c.state.Style {
		changes |= ChangeStyle
	}
	if !next.Filter.Same(c.state.Filter) || next.Account != c.state.Account {
		changes |= ChangeFilters
	}
	if next.Selection != c.state.Selection || next.RevealsWhy != c.state.RevealsWhy ||
		view.ShowsPanel != c.view.ShowsPanel {
		changes |= ChangeSelection
	}
	if contentDiffers(view, c.view) {
		changes |= ChangeContent
	}
	c.state = next
	c.view = view
	c.deliver(changes)
	c.requestMessages()
}

func hasAccount(s Snapshot, id api.AccountID) bool {
	for _, a := range s.Accounts {
		if a.ID == id {
			return true
		}
	}
	return false
}

// requestMessages asks the source for the selected case's conversation
// when another case is selected or the selected one changed since it last
// asked.
func (c *Controller) requestMessages() {
	snapshot := c.source.Snapshot()
	id := c.state.Selection
	k, ok := snapshot.Case(id)
	if id == "" || !ok {
		c.requested = nil
		return
	}
	phase := snapshot.Phase
	if r := c.requested; r != nil && r.id == id && r.version == k.Version {
		// The same case: asked again only when its load failed and the
		// board has since come back from a failure (a reconnect).
		if !k.MessagesFailed || r.phase == phase || !r.phase.IsFailure() || phase.IsFailure() {
			c.requested = &requested{id: id, version: k.Version, phase: phase}
			return
		}
	}
	c.requested = &requested{id: id, version: k.Version, phase: phase}
	c.source.LoadMessages(id)
}

// failedLoad reports whether the conversation of case id could not be
// loaded.
func (c *Controller) failedLoad(id CaseID) bool {
	if id == "" {
		return false
	}
	k, ok := c.source.Snapshot().Case(id)
	return ok && k.MessagesFailed
}

// deliver tells OnChange about changes. Called again from inside OnChange,
// it only collects them: the outer delivery hands them over once the
// listener returns, as many times as new ones came, each time with the
// view model as it is then.
func (c *Controller) deliver(changes Changes) {
	c.pending |= changes
	if c.notifying {
		return
	}
	c.notifying = true
	defer func() { c.notifying = false }()
	for c.pending != 0 {
		next := c.pending
		c.pending = 0
		if c.OnChange != nil {
			c.OnChange(next)
		}
	}
}

// contentDiffers reports whether the view models differ beyond the
// selection: another selected case's detail is a selection change, the
// same case's is content.
func contentDiffers(a, b ViewModel) bool {
	a.Selection, b.Selection = "", ""
	a.ShowsPanel, b.ShowsPanel = false, false
	if detailID(a.Detail) != detailID(b.Detail) {
		a.Detail, b.Detail = nil, nil
	}
	return !reflect.DeepEqual(a, b)
}

func detailID(d *Detail) CaseID {
	if d == nil {
		return ""
	}
	return d.ID
}
