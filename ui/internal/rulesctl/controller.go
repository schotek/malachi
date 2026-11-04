// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package rulesctl

import (
	"encoding/json"
	"slices"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/rules"
)

// Controller keeps the list of rules (rules.list, again on every
// notify.rulesChanged) for the Rules page of Preferences and for the
// window's context menus, and does what the page's rows and the folder
// menu's Apply Rules… do:
//
//   - Toggle: the switch of a row, rules.update with Enabled flipped, asking
//     OutgoingQuestion first when rules.NeedsConfirm says so (and sending
//     confirmOutgoing only after the answer);
//   - Move / MoveBy: a drag or Alt+Up/Down, rules.reorder with rules.Moved;
//   - Remove: DeleteQuestion, then rules.delete;
//   - Apply: ApplyQuestion, then rules.apply with the progress line
//     (OnBusy) and the result as a toast.
//
// Each change is shown at once and undone when the daemon refuses it. The
// view reads Rows after OnChange.
type Controller struct {
	Opts Options
	// Dir names the accounts and folders of the rows and the editor; Start
	// loads it (a window that has them gives it Seed).
	Dir *Directory

	// OnChange: the list changed; the view reads it anew.
	OnChange func()
	// OnQuestion asks the user and calls answer with the reply; without it
	// nothing that needs a confirmation goes ahead.
	OnQuestion func(q rules.Question, answer func(ok bool))
	// OnToast shows a sentence (a refusal, the result of Apply).
	OnToast func(text string)
	// OnBusy shows the progress line while rules.apply runs; "" ends it.
	OnBusy func(text string)

	rules    []api.Rule
	loaded   bool
	missing  bool // the daemon has no rules.list
	err      string
	gen      int
	applying bool
	busy     map[string]bool // rules with a write in flight
	closed   bool
	stopDir  func()
}

// NewController is a controller with an empty Directory; Start loads it.
func NewController(opts Options) *Controller {
	return &Controller{Opts: opts, Dir: NewDirectory(opts), busy: map[string]bool{}}
}

// Start loads the rules and the accounts and folders they name.
func (c *Controller) Start() {
	c.stopDir = c.Dir.Listen(c.changed)
	c.Dir.Load()
	c.Refresh()
}

// Stop ends the controller.
func (c *Controller) Stop() {
	c.closed = true
	if c.stopDir != nil {
		c.stopDir()
	}
	c.Dir.Stop()
}

// Loaded reports the first answer of rules.list.
func (c *Controller) Loaded() bool { return c.loaded }

// Available reports that the daemon has the rules (an older one answers
// methodNotFound): the menus offer nothing without them.
func (c *Controller) Available() bool { return !c.missing }

// Error is the sentence of the last failed load ("" none).
func (c *Controller) Error() string { return c.err }

// Rules are the rules in their order.
func (c *Controller) Rules() []api.Rule { return c.rules }

// Rule is the rule with the id.
func (c *Controller) Rule(id string) (api.Rule, bool) {
	for _, r := range c.rules {
		if r.ID == id {
			return r, true
		}
	}
	return api.Rule{}, false
}

// Rows are the rows of the page.
func (c *Controller) Rows() []rules.Row {
	return rules.Rows(c.rules, c.Dir.Names(), c.Opts.Date, c.Opts.Tr)
}

// Busy reports a write in flight for the rule (its row is insensitive).
func (c *Controller) Busy(id string) bool { return c.busy[id] }

// Applying reports a running Apply.
func (c *Controller) Applying() bool { return c.applying }

func (c *Controller) changed() {
	if c.OnChange != nil && !c.closed {
		c.OnChange()
	}
}

func (c *Controller) toast(text string) {
	if text != "" && c.OnToast != nil && !c.closed {
		c.OnToast(text)
	}
}

// Handle reloads after notify.rulesChanged and the directory after
// notify.accountsChanged.
func (c *Controller) Handle(method string, _ json.RawMessage) {
	switch method {
	case api.NotifyRulesChanged:
		c.Refresh()
	case api.NotifyAccountsChanged:
		if c.Dir.Loaded() {
			c.Dir.Load()
		}
	}
}

// Refresh reads rules.list again; the answer of the latest call wins.
func (c *Controller) Refresh() {
	if c.closed {
		return
	}
	c.gen++
	gen := c.gen
	call(c.Opts, api.MethodRulesList, api.RulesListParams{}, 0, func(r *api.RulesListResult, e *api.Error) {
		if c.closed || gen != c.gen {
			return
		}
		if e != nil {
			c.missing = e.Code == api.CodeMethodNotFound
			if !c.missing {
				c.err = c.Opts.otherErrorText(e)
			}
			c.changed()
			return
		}
		c.missing, c.err = false, ""
		c.rules, c.loaded = r.Rules, true
		c.changed()
	})
}

// replace puts the daemon's rule in the list in place of the one with its
// id.
func (c *Controller) replace(r api.Rule) {
	for i := range c.rules {
		if c.rules[i].ID == r.ID {
			c.rules[i] = r
		}
	}
}

// Toggle turns the rule on or off (the switch of its row). A rule that
// sends mail asks first; a refusal puts the switch back.
func (c *Controller) Toggle(id string, on bool) {
	r, ok := c.Rule(id)
	if !ok || r.Enabled == on || c.busy[id] {
		c.changed()
		return
	}
	in := rules.Toggled(r)
	send := func(confirm bool) {
		c.busy[id] = true
		c.setEnabled(id, on)
		c.changed()
		call(c.Opts, api.MethodRulesUpdate, api.RulesUpdateParams{ID: id, Rule: in, ConfirmOutgoing: confirm}, 0,
			func(res *api.RulesUpdateResult, e *api.Error) {
				delete(c.busy, id)
				if c.closed {
					return
				}
				if e != nil {
					c.setEnabled(id, !on)
					c.toast(SaveErrorText(e, c.Opts.Tr))
					if e.Code == api.CodeRuleNotFound {
						c.Refresh()
					}
					c.changed()
					return
				}
				c.replace(res.Rule)
				c.changed()
			})
	}
	if !rules.NeedsConfirm(&r, in) {
		send(false)
		return
	}
	if c.OnQuestion == nil {
		c.changed()
		return
	}
	c.OnQuestion(rules.OutgoingQuestion(in, c.Opts.Tr), func(ok bool) {
		if ok {
			send(true)
		} else {
			c.changed() // the view puts the switch back
		}
	})
}

func (c *Controller) setEnabled(id string, on bool) {
	for i := range c.rules {
		if c.rules[i].ID == id {
			c.rules[i].Enabled = on
		}
	}
}

// IDs are the ids of the rules in their order.
func (c *Controller) IDs() []string {
	ids := make([]string, len(c.rules))
	for i, r := range c.rules {
		ids[i] = r.ID
	}
	return ids
}

// Index is the position of the rule, -1 when there is none.
func (c *Controller) Index(id string) int { return slices.Index(c.IDs(), id) }

// MoveBy shifts the rule by delta positions (Alt+Up / Alt+Down); it
// reports whether it moved anything.
func (c *Controller) MoveBy(id string, delta int) bool {
	from := c.Index(id)
	if from < 0 {
		return false
	}
	to := from + delta
	if to < 0 || to >= len(c.rules) {
		return false
	}
	c.Move(from, to)
	return true
}

// Move drops the rule at from at to (an index of the list without it):
// shown at once, saved with rules.reorder, put back when refused.
func (c *Controller) Move(from, to int) {
	ids := c.IDs()
	moved := rules.Moved(ids, from, to)
	if slices.Equal(ids, moved) {
		return
	}
	byID := make(map[string]api.Rule, len(c.rules))
	for _, r := range c.rules {
		byID[r.ID] = r
	}
	before := c.rules
	c.rules = make([]api.Rule, len(moved))
	for i, id := range moved {
		c.rules[i] = byID[id]
	}
	c.gen++ // an older list must not undo it
	c.changed()
	call(c.Opts, api.MethodRulesReorder, api.RulesReorderParams{IDs: moved}, 0, func(res *api.RulesReorderResult, e *api.Error) {
		if c.closed {
			return
		}
		if e != nil {
			c.rules = before
			c.toast(SaveErrorText(e, c.Opts.Tr))
			c.changed()
			c.Refresh()
			return
		}
		c.rules = res.Rules
		c.changed()
	})
}

// Remove asks DeleteQuestion and then deletes the rule.
func (c *Controller) Remove(id string) {
	r, ok := c.Rule(id)
	if !ok || c.OnQuestion == nil {
		return
	}
	c.OnQuestion(rules.DeleteQuestion(r.Name, c.Opts.Tr), func(ok bool) {
		if !ok {
			return
		}
		c.busy[id] = true
		c.changed()
		call(c.Opts, api.MethodRulesDelete, api.RulesDeleteParams{ID: id}, 0, func(_ *api.RulesDeleteResult, e *api.Error) {
			delete(c.busy, id)
			if c.closed {
				return
			}
			if e != nil {
				c.toast(c.Opts.otherErrorText(e))
				c.Refresh()
				c.changed()
				return
			}
			c.rules = slices.DeleteFunc(slices.Clone(c.rules), func(x api.Rule) bool { return x.ID == id })
			c.changed()
		})
	})
}

// Apply asks ApplyQuestion for the folder (named folderName) and runs the
// rules of its account on it by hand: the progress line while it runs,
// the result as a toast.
func (c *Controller) Apply(account api.AccountID, folder api.FolderID, folderName string) {
	if c.applying || c.OnQuestion == nil {
		return
	}
	c.OnQuestion(rules.ApplyQuestion(folderName, c.Opts.Tr), func(ok bool) {
		if !ok || c.applying || c.closed {
			return
		}
		c.applying = true
		if c.OnBusy != nil {
			c.OnBusy(rules.ApplyingText(c.Opts.Tr))
		}
		call(c.Opts, api.MethodRulesApply, api.RulesApplyParams{AccountID: account, FolderID: folder}, ApplyTimeout,
			func(res *api.RulesApplyResult, e *api.Error) {
				c.applying = false
				if c.OnBusy != nil {
					c.OnBusy("")
				}
				if c.closed {
					return
				}
				if e != nil {
					c.toast(rules.ApplyFailedText(c.Opts.Tr))
					return
				}
				c.toast(rules.ApplyResultText(*res, c.Opts.Tr))
			})
	})
}

// AppliesTo reports whether an enabled rule runs on the account's mail
// (the folder menu's Apply Rules… is on then).
func (c *Controller) AppliesTo(account api.AccountID) bool {
	return rules.AppliesToAccount(c.rules, account)
}
