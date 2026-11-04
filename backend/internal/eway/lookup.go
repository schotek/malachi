// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eway

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// LookupBudget bounds a whole lookup (D9).
	LookupBudget = 15 * time.Second
	// cacheTTL is how long what one address found is reused.
	cacheTTL = 10 * time.Minute
	// maxCached bounds the address cache of one client.
	maxCached = 2000
	// maxOpenPerKind caps the deals and the projects suggested.
	maxOpenPerKind = 10
)

// Suggestion is an item the addresses lead to, with why.
type Suggestion struct {
	Item
	Reason   api.CrmReason
	Superior bool // the one open deal or project, pre-checked as superior
}

// Suggestions is Suggest's result.
type Suggestions struct {
	Items   []Suggestion
	Partial bool // the budget ran out, or a step failed: something may be missing
}

type cachedAddress struct {
	at        time.Time
	contacts  []Item
	companies []Item // by e-mail
	domain    []Item // by tracked domain
}

// rank orders the reasons (address > domain > via a contact > via a
// company).
func rank(r api.CrmReason) int {
	switch r {
	case api.CrmReasonAddress:
		return 0
	case api.CrmReasonDomain:
		return 1
	case api.CrmReasonContact:
		return 2
	}
	return 3
}

// Suggest finds what the addresses (already cleaned, the user's own left
// out) lead to (D9): contacts by e-mail, companies by e-mail and by
// tracked domain (not for freemail domains), the companies of the
// contacts, and the open deals and projects related to any of them, at
// most maxOpenPerKind each, the most recently changed first. Four
// requests at a time within budget; what is not done by then is left out
// and Partial set. An authentication problem is the error; other failures
// make the result partial.
func (c *Client) Suggest(ctx context.Context, addrs []string, budget time.Duration) (Suggestions, error) {
	if budget <= 0 {
		budget = LookupBudget
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	// Sign in first so that a refused sign-in is the answer, not a
	// partial nothing.
	if _, err := c.logIn(ctx); err != nil {
		return Suggestions{}, err
	}
	var (
		mu      sync.Mutex
		partial bool
		fatal   error
		found   = map[string]*Suggestion{}
		order   []string
	)
	add := func(it Item, reason api.CrmReason) {
		mu.Lock()
		defer mu.Unlock()
		if s, ok := found[it.GUID]; ok {
			if rank(reason) < rank(s.Reason) {
				s.Reason = reason
			}
			if s.Detail == "" {
				s.Detail = it.Detail
			}
			return
		}
		found[it.GUID] = &Suggestion{Item: it, Reason: reason}
		order = append(order, it.GUID)
	}
	fail := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if isFatal(err) && fatal == nil {
			fatal = err
		}
		partial = true
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrent)
	for _, a := range addrs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				fail(ctx.Err())
				return
			}
			defer func() { <-sem }()
			r, err := c.address(ctx, a)
			if err != nil {
				fail(err)
			}
			for _, it := range r.contacts {
				add(it, api.CrmReasonAddress)
			}
			for _, it := range r.companies {
				add(it, api.CrmReasonAddress)
			}
			for _, it := range r.domain {
				add(it, api.CrmReasonDomain)
			}
		}()
	}
	wg.Wait()
	if fatal != nil {
		return Suggestions{}, fatal
	}

	// The contacts with their company and relations, the companies with
	// their relations.
	var contacts, companies []string
	for _, g := range order {
		switch found[g].Kind {
		case api.CrmContact:
			contacts = append(contacts, g)
		case api.CrmCompany:
			companies = append(companies, g)
		}
	}
	related := map[api.CrmItemKind]map[string]api.CrmReason{api.CrmDeal: {}, api.CrmProject: {}}
	note := func(it Item, via api.CrmReason) {
		for _, r := range it.Relations {
			if m, ok := related[r.Kind]; ok {
				if old, had := m[r.ForeignGUID]; !had || rank(via) < rank(old) {
					m[r.ForeignGUID] = via
				}
			}
		}
	}
	if len(contacts) > 0 {
		items, err := c.Items(ctx, api.CrmContact, contacts)
		if err != nil {
			fail(err)
		}
		var viaContact []string
		for _, it := range items {
			note(it, api.CrmReasonContact)
			if it.CompanyGUID != "" {
				if _, ok := found[it.CompanyGUID]; !ok {
					viaContact = append(viaContact, it.CompanyGUID)
				}
			}
		}
		if len(viaContact) > 0 {
			got, err := c.Items(ctx, api.CrmCompany, viaContact)
			if err != nil {
				fail(err)
			}
			for _, it := range got {
				add(it, api.CrmReasonContact)
				companies = append(companies, it.GUID)
				note(it, api.CrmReasonCompany)
			}
		}
	}
	if len(companies) > 0 {
		items, err := c.Items(ctx, api.CrmCompany, companies)
		if err != nil {
			fail(err)
		}
		for _, it := range items {
			note(it, api.CrmReasonCompany)
		}
	}
	var open []Suggestion
	for _, kind := range []api.CrmItemKind{api.CrmDeal, api.CrmProject} {
		var guids []string
		for g := range related[kind] {
			guids = append(guids, g)
		}
		if len(guids) == 0 {
			continue
		}
		sort.Strings(guids)
		items, err := c.Items(ctx, kind, guids)
		if err != nil {
			fail(err)
			continue
		}
		var keep []Item
		for _, it := range items {
			if it.Open {
				keep = append(keep, it)
			}
		}
		sort.SliceStable(keep, func(i, j int) bool { return keep[i].Changed.After(keep[j].Changed) })
		if len(keep) > maxOpenPerKind {
			keep = keep[:maxOpenPerKind]
		}
		for _, it := range keep {
			open = append(open, Suggestion{Item: it, Reason: related[kind][it.GUID]})
		}
	}
	if fatal != nil {
		return Suggestions{}, fatal
	}

	out := make([]Suggestion, 0, len(order)+len(open))
	for _, g := range order {
		out = append(out, *found[g])
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i].Reason) < rank(out[j].Reason) })
	if len(open) == 1 {
		open[0].Superior = true // the Outlook add-in's rule (§5 Q2)
	}
	out = append(out, open...)
	if len(out) > api.MaxCrmSuggestions {
		out = out[:api.MaxCrmSuggestions]
	}
	return Suggestions{Items: out, Partial: partial || ctx.Err() != nil}, nil
}

// isFatal: a failure that ends the lookup (the sign-in), not one that
// makes it partial.
func isFatal(err error) bool {
	var ae *api.Error
	if errors.As(err, &ae) {
		switch ae.Code {
		case api.CodeAuthFailed, api.CodeAuthRequired, api.CodeKeyringError, api.CodeCrmSignInUnsupported, api.CodeCancelled:
			return true
		}
	}
	return false
}

// address is what one address finds, from the cache when fresh. A
// failure is returned with what was found before it (not cached).
func (c *Client) address(ctx context.Context, a string) (cachedAddress, error) {
	c.cacheMu.Lock()
	if r, ok := c.cache[a]; ok && c.now().Sub(r.at) < cacheTTL {
		c.cacheMu.Unlock()
		return r, nil
	}
	c.cacheMu.Unlock()
	var r cachedAddress
	var err error
	if r.contacts, err = c.ContactsByEmail(ctx, a); err != nil {
		return r, err
	}
	if r.companies, err = c.CompaniesByEmail(ctx, a); err != nil {
		return r, err
	}
	if d := DomainOf(a); d != "" && !Freemail(d) {
		if r.domain, err = c.CompaniesByDomain(ctx, d); err != nil {
			return r, err
		}
	}
	r.at = c.now()
	c.cacheMu.Lock()
	if len(c.cache) >= maxCached {
		for k, v := range c.cache {
			if c.now().Sub(v.at) >= cacheTTL || len(c.cache) >= maxCached {
				delete(c.cache, k)
			}
		}
	}
	c.cache[a] = r
	c.cacheMu.Unlock()
	return r, nil
}

// ForgetCache drops what the address cache holds (after a save changed
// the CRM, or the account's settings).
func (c *Client) ForgetCache() {
	c.cacheMu.Lock()
	c.cache = map[string]cachedAddress{}
	c.cacheMu.Unlock()
}
