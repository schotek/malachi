// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"slices"

	"github.com/schotek/malachi/backend/pkg/api"
)

// ConversationMaxLiveWebViews leaves room for the detail's inline editor.
// Port of MalachiCore/Board/BoardConversationCards.swift; the GTK cards
// apply these decisions without owning folding or eviction policy.
const ConversationMaxLiveWebViews = 4

// ConversationMember identifies a card and the excerpt it represents.
// ID is empty for the invented samples, which never request bodies.
type ConversationMember struct {
	ID   api.MessageID
	Text string
}

// ConversationKey excludes unrelated detail changes such as draft autosaves.
type ConversationKey struct {
	CaseID  CaseID
	Members []ConversationMember
}

// ConversationKeyOf is the part of a detail that changes its cards.
func ConversationKeyOf(d Detail) ConversationKey {
	k := ConversationKey{CaseID: d.ID, Members: make([]ConversationMember, len(d.Messages))}
	for i, m := range d.Messages {
		k.Members[i] = ConversationMember{ID: m.ID, Text: m.Text}
	}
	return k
}

// ConversationBody records an attempted body fetch, including silent text
// fallback for withheld HTML, missing bodies, unavailable views and errors.
type ConversationBody int

const (
	ConversationBodyUnknown ConversationBody = iota
	ConversationBodyAsked
	ConversationBodyHTML
	ConversationBodyText
)

// ConversationShows is what a card displays while the detail is live.
type ConversationShows int

const (
	ConversationFolded ConversationShows = iota
	ConversationText
	ConversationWeb
)

// ConversationChangeKind identifies the work a card refresh requires.
type ConversationChangeKind int

const (
	ConversationChangeNone ConversationChangeKind = iota
	ConversationChangeReset
	ConversationChangeMembers
)

// ConversationChange preserves views by matching new indices to old ones.
// Reload contains open kept cards whose excerpt changed: keep their old
// body visible while asking for a fresh one. Unnamed previous cards go.
type ConversationChange struct {
	Kind   ConversationChangeKind
	Kept   map[int]int
	Reload []int
}

// ConversationCards is the fold/body/LRU state for one detail, oldest
// member first. The newest and the card just opened are never evicted.
// Thus a deliberately tiny cap may be exceeded while both are protected.
// The zero value works with the default cap.
type ConversationCards struct {
	maxLive int
	caseID  CaseID
	hasCase bool
	members []ConversationMember
	folds   []bool
	bodies  map[ConversationMember]ConversationBody
	opened  []ConversationMember
}

// NewConversationCards selects the HTML view cap. Zero uses the default;
// a negative cap is clamped to one.
func NewConversationCards(maxLive int) *ConversationCards {
	if maxLive == 0 {
		maxLive = ConversationMaxLiveWebViews
	}
	return &ConversationCards{maxLive: max(maxLive, 1)}
}

func (c *ConversationCards) MaxLive() int {
	if c.maxLive == 0 {
		return ConversationMaxLiveWebViews
	}
	return c.maxLive
}

func (c *ConversationCards) CaseID() CaseID                { return c.caseID }
func (c *ConversationCards) Members() []ConversationMember { return slices.Clone(c.members) }
func (c *ConversationCards) Newest() int                   { return len(c.members) - 1 }
func (c *ConversationCards) valid(i int) bool              { return i >= 0 && i < len(c.members) }

// Apply keeps exact members first, in order (even repeated sample text),
// then matches changed excerpts by nonempty message ID. A new newest opens;
// all other new cards fold. Existing cards keep their fold and body state.
func (c *ConversationCards) Apply(key ConversationKey) ConversationChange {
	if !c.hasCase || c.caseID != key.CaseID {
		c.caseID, c.hasCase = key.CaseID, true
		c.members = slices.Clone(key.Members)
		c.folds = make([]bool, len(c.members))
		for i := range c.folds {
			c.folds[i] = i != c.Newest()
		}
		c.bodies = make(map[ConversationMember]ConversationBody)
		c.opened = nil
		if len(c.members) > 0 {
			c.opened = append(c.opened, c.members[c.Newest()])
		}
		return ConversationChange{Kind: ConversationChangeReset}
	}
	if slices.Equal(key.Members, c.members) {
		return ConversationChange{Kind: ConversationChangeNone}
	}
	free := make(map[ConversationMember][]int)
	for i, m := range c.members {
		free[m] = append(free[m], i)
	}
	kept := make(map[int]int)
	used := make(map[int]bool)
	for i, m := range key.Members {
		if slots := free[m]; len(slots) > 0 {
			kept[i], used[slots[0]], free[m] = slots[0], true, slots[1:]
		}
	}
	changed := make(map[int]bool)
	for i, m := range key.Members {
		if _, ok := kept[i]; ok || m.ID == "" {
			continue
		}
		for old, prior := range c.members {
			if !used[old] && prior.ID == m.ID {
				kept[i], used[old], changed[i] = old, true, true
				break
			}
		}
	}
	folds := make([]bool, len(key.Members))
	carried := make(map[ConversationMember]ConversationBody)
	renamed := make(map[ConversationMember]ConversationMember)
	var reload []int
	for i, m := range key.Members {
		old, exists := kept[i]
		if !exists {
			folds[i] = i != len(key.Members)-1
			if !folds[i] {
				c.removeOpened(m)
				c.opened = append(c.opened, m)
			}
			continue
		}
		folds[i] = c.folds[old]
		if !changed[i] {
			continue
		}
		prior := c.members[old]
		renamed[prior] = m
		if b := c.bodies[prior]; !c.folds[old] && (b == ConversationBodyHTML || b == ConversationBodyText) {
			carried[m] = b
			reload = append(reload, i)
		}
	}
	present := make(map[ConversationMember]bool)
	for _, m := range key.Members {
		present[m] = true
	}
	for i, m := range c.opened {
		if replacement, ok := renamed[m]; ok {
			c.opened[i] = replacement
		}
	}
	// Removing (or reordering) the newest can promote a folded kept card.
	// The newest has no fold arrow, so preserving that fold would leave it
	// permanently collapsed. Keep the newest-open invariant on promotion.
	promoted := false
	if last := len(folds) - 1; last >= 0 && folds[last] {
		promoted = true
		folds[last] = false
		m := key.Members[last]
		c.removeOpened(m)
		c.opened = append(c.opened, m)
	}
	c.opened = slices.DeleteFunc(c.opened, func(m ConversationMember) bool { return !present[m] })
	c.members, c.folds = slices.Clone(key.Members), folds
	for m := range c.bodies {
		if !present[m] {
			delete(c.bodies, m)
		}
	}
	for m, b := range carried {
		c.bodies[m] = b
	}
	// A promoted card may already have cached HTML; it counts immediately,
	// without waiting for another answer that it does not need to request.
	if promoted {
		c.enforce(c.Newest())
	}
	return ConversationChange{Kind: ConversationChangeMembers, Kept: kept, Reload: reload}
}

func (c *ConversationCards) IsFolded(i int) bool { return c.valid(i) && c.folds[i] }
func (c *ConversationCards) Foldable(i int) bool { return c.valid(i) && i != c.Newest() }

// Arrow offers opening to fetch formatted mail even for a short excerpt;
// text-only cards offer it only after measuring overflow of their preview.
func (c *ConversationCards) Arrow(i int, long *bool, canFetch bool) bool {
	return c.Foldable(i) && ((canFetch && c.members[i].ID != "") || (long != nil && *long))
}

func (c *ConversationCards) Body(i int) ConversationBody {
	if !c.valid(i) {
		return ConversationBodyUnknown
	}
	return c.bodies[c.members[i]]
}

func (c *ConversationCards) NeedsBody(i int) bool {
	return c.valid(i) && c.members[i].ID != "" && !c.folds[i] && c.Body(i) == ConversationBodyUnknown
}

func (c *ConversationCards) Shows(i int, live bool) ConversationShows {
	if !c.valid(i) {
		return ConversationText
	}
	if c.folds[i] {
		return ConversationFolded
	}
	if live && c.Body(i) == ConversationBodyHTML {
		return ConversationWeb
	}
	return ConversationText
}

func (c *ConversationCards) WebCards() []int {
	var indices []int
	for i := range c.members {
		if !c.folds[i] && c.Body(i) == ConversationBodyHTML {
			indices = append(indices, i)
		}
	}
	return indices
}

func (c *ConversationCards) Index(id api.MessageID) (int, bool) {
	for i, m := range c.members {
		if m.ID == id {
			return i, true
		}
	}
	return 0, false
}

func (c *ConversationCards) Asked(i int) {
	if c.valid(i) && c.Body(i) == ConversationBodyUnknown {
		c.bodies[c.members[i]] = ConversationBodyAsked
	}
}

// Answered returns indices folded to respect the cap, in display order.
func (c *ConversationCards) Answered(i int, html bool) []int {
	if !c.valid(i) {
		return nil
	}
	c.bodies[c.members[i]] = ConversationBodyText
	if html {
		c.bodies[c.members[i]] = ConversationBodyHTML
	}
	if !html || c.folds[i] {
		return nil
	}
	return c.enforce(i)
}

func (c *ConversationCards) SetFolded(i int, folded bool) []int {
	if !c.Foldable(i) || c.folds[i] == folded {
		return nil
	}
	c.folds[i] = folded
	m := c.members[i]
	c.removeOpened(m)
	if folded {
		return nil
	}
	c.opened = append(c.opened, m)
	if c.Body(i) == ConversationBodyHTML {
		return c.enforce(i)
	}
	return nil
}

func (c *ConversationCards) removeOpened(m ConversationMember) {
	c.opened = slices.DeleteFunc(c.opened, func(old ConversationMember) bool { return old == m })
}

func (c *ConversationCards) enforce(protected int) []int {
	var folded []int
	web := c.WebCards()
	for len(web) > c.MaxLive() {
		victim, rank := -1, 0
		for _, i := range web {
			if i == c.Newest() || i == protected {
				continue
			}
			r := slices.Index(c.opened, c.members[i])
			// web is in index order, so equal recency prefers the first.
			if victim < 0 || r < rank {
				victim, rank = i, r
			}
		}
		if victim < 0 {
			break
		}
		c.folds[victim] = true
		c.removeOpened(c.members[victim])
		folded = append(folded, victim)
		web = slices.DeleteFunc(web, func(i int) bool { return i == victim })
	}
	slices.Sort(folded)
	return folded
}

// ConversationCompensatedTop holds the viewport on the same content when
// a card ending above it changes height. All coordinates are document
// coordinates before that change, except documentHeight (after it).
func ConversationCompensatedTop(viewportTop, cardMaxY, delta, documentHeight, viewportHeight float64) float64 {
	move := 0.0
	if cardMaxY <= viewportTop+0.5 {
		move = delta
	}
	return min(max(0, viewportTop+move), max(0, documentHeight-viewportHeight))
}
