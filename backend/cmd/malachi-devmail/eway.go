// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/sha256"
	"fmt"
	"net"
	"strings"

	"github.com/schotek/malachi/backend/internal/eway/ewaytest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// EwaySet is the CRM of a sample set: an eWay-CRM instance
// (internal/eway/ewaytest, user "user", password "secret", path /acme)
// with these companies, contacts, deals and projects, and the messages
// the user has saved to it.
type EwaySet struct {
	Companies map[string]EwayCompany `json:"companies"`
	Contacts  map[string]EwayContact `json:"contacts"`
	Deals     map[string]EwayItem    `json:"deals"`
	Projects  map[string]EwayItem    `json:"projects"`
	// Saved names messages of the mailbox saved to the CRM with links
	// ("contact:petr", "company:acme") and a superior deal or project
	// ("deal:warehouse"); add-account performs the saves over RPC.
	Saved []EwaySaved `json:"saved,omitempty"`
}

// EwayCompany is a company; Domain makes the CRM suggest it for mail
// from that domain.
type EwayCompany struct {
	FileAs string `json:"fileAs"`
	Email  string `json:"email,omitempty"`
	Domain string `json:"domain,omitempty"`
}

// EwayContact is a contact of a person of the story, at a company.
type EwayContact struct {
	FileAs  string `json:"fileAs"`
	Person  string `json:"person"`
	Company string `json:"company,omitempty"`
}

// EwayItem is a deal or a project.
type EwayItem struct {
	FileAs    string `json:"fileAs"`
	Company   string `json:"company,omitempty"`
	Completed bool   `json:"completed,omitempty"`
}

// EwaySaved is a message saved to the CRM.
type EwaySaved struct {
	Message  string   `json:"message"`
	Link     []string `json:"link,omitempty"`
	Superior string   `json:"superior,omitempty"`
}

// Credentials of the fake instance.
const (
	ewayUser     = "user"
	ewayPassword = "secret"
	ewayPath     = "/acme"
)

// ewayGUID is the GUID of a sample item, the same in every process (serve
// seeds the instance with it, add-account names it in crm.save).
func ewayGUID(kind, key string) string {
	h := sha256.Sum256([]byte("malachi-devmail eway " + kind + ":" + key))
	b := h[:16]
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ewayRef resolves "kind:key" of a sample set to a crm.save item.
func ewayRef(set *EwaySet, ref string) (api.CrmItemRef, error) {
	kind, key, ok := strings.Cut(ref, ":")
	if !ok {
		return api.CrmItemRef{}, fmt.Errorf("eway: bad item reference %q (want kind:key)", ref)
	}
	var exists bool
	var k api.CrmItemKind
	switch kind {
	case "contact":
		_, exists = set.Contacts[key]
		k = api.CrmContact
	case "company":
		_, exists = set.Companies[key]
		k = api.CrmCompany
	case "deal":
		_, exists = set.Deals[key]
		k = api.CrmDeal
	case "project":
		_, exists = set.Projects[key]
		k = api.CrmProject
	default:
		return api.CrmItemRef{}, fmt.Errorf("eway: unknown item kind %q", kind)
	}
	if !exists {
		return api.CrmItemRef{}, fmt.Errorf("eway: no %s %q in the sample set", kind, key)
	}
	return api.CrmItemRef{Kind: k, GUID: ewayGUID(kind, key)}, nil
}

// startEway starts the fake instance on a loopback port and fills it with
// the set.
func startEway(c *Corpus, port int) (*ewaytest.Server, int, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, 0, fmt.Errorf("eway: %w", err)
	}
	f := ewaytest.NewStandalone(ln)
	f.SetUserEmail(c.Me.Email)
	set := c.Eway
	for key, co := range set.Companies {
		f.Add("Companies", ewaytest.Item{GUID: ewayGUID("company", key), FileAs: co.FileAs, Email: co.Email, TrackedDomains: co.Domain})
	}
	for key, ct := range set.Contacts {
		p := c.person(ct.Person)
		it := ewaytest.Item{GUID: ewayGUID("contact", key), FileAs: ct.FileAs, Email1: p.Email}
		if ct.Company != "" {
			it.CompanyGUID = ewayGUID("company", ct.Company)
		}
		f.Add("Contacts", it)
	}
	for folder, items := range map[string]map[string]EwayItem{"Leads": set.Deals, "Projects": set.Projects} {
		kind := "deal"
		if folder == "Projects" {
			kind = "project"
		}
		for key, it := range items {
			guid := ewayGUID(kind, key)
			f.Add(folder, ewaytest.Item{GUID: guid, FileAs: it.FileAs, IsCompleted: it.Completed})
			if it.Company != "" {
				f.Relate(guid, folder, ewayGUID("company", it.Company), "Companies", "GENERAL")
			}
		}
	}
	return f, ln.Addr().(*net.TCPAddr).Port, nil
}
