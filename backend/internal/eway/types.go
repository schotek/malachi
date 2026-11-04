// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eway

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// eWay-CRM's folder (module) names.
const (
	FolderEmails    = "Emails"
	FolderContacts  = "Contacts"
	FolderCompanies = "Companies"
	FolderLeads     = "Leads"
	FolderProjects  = "Projects"
)

// Relation types (docs/eway.md D10; T0 4).
const (
	RelationGeneral  = "GENERAL"
	RelationSuperior = "OUTLOOKPROJECT"
)

// Return codes of eWay-CRM's answers this client tells apart. Any other
// non-success code is a server error; only its code is ever logged or
// put in an error, never the answer's Description (which can carry the
// session id, T0 3).
const (
	rcSuccess           = "rcSuccess"
	rcBadSession        = "rcBadSession"
	rcLoginFailed       = "rcLoginFailed"
	rcDuplicateEmail    = "rcDuplicateEmail"
	rcDuplicateRelation = "rcDuplicateRelation"
)

// FolderOf is the eWay-CRM folder of an item kind, "" for none.
func FolderOf(k api.CrmItemKind) string {
	switch k {
	case api.CrmContact:
		return FolderContacts
	case api.CrmCompany:
		return FolderCompanies
	case api.CrmDeal:
		return FolderLeads
	case api.CrmProject:
		return FolderProjects
	}
	return ""
}

// KindOf is the item kind of an eWay-CRM folder, "" for another folder.
func KindOf(folder string) api.CrmItemKind {
	switch {
	case strings.EqualFold(folder, FolderContacts):
		return api.CrmContact
	case strings.EqualFold(folder, FolderCompanies):
		return api.CrmCompany
	case strings.EqualFold(folder, FolderLeads):
		return api.CrmDeal
	case strings.EqualFold(folder, FolderProjects):
		return api.CrmProject
	}
	return ""
}

// Ref names an item: its kind and GUID (lower case).
type Ref struct {
	Kind api.CrmItemKind
	GUID string
}

// Relation is one relation of an item, as Get…ByItemGuids
// includeRelations returns it.
type Relation struct {
	Kind         api.CrmItemKind // of the foreign item; "" for a folder this client does not use
	ForeignGUID  string
	Type         string // RelationGeneral, RelationSuperior or another
	RelationData string // RelationDataGUID, what DeleteRelation takes
}

// Item is an item of the CRM, cleaned.
type Item struct {
	Ref
	Name   string
	Email  string // contacts and companies: the first usable address
	Detail string
	// CompanyGUID is a contact's company (Companies_CompanyGuid), "".
	CompanyGUID string
	// Open: a deal or project neither completed nor lost (T0 8); true for
	// other kinds.
	Open      bool
	Changed   time.Time // ItemChanged, zero when unknown
	Relations []Relation
}

// envelope is the frame of every answer: the return code and the payload
// under one of several names.
type envelope struct {
	ReturnCode   flexString      `json:"ReturnCode"`
	Data         json.RawMessage `json:"Data"`
	Datum        json.RawMessage `json:"Datum"`
	Guid         flexString      `json:"Guid"`
	SessionID    flexString      `json:"SessionId"`
	UserItemGUID flexString      `json:"UserItemGuid"`
	WcfVersion   flexString      `json:"WcfVersion"`
	IsAzureAuth  *bool           `json:"IsAzureAuth"`
}

// flexString accepts a JSON string or null; anything else is an error.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		*f = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*f = flexString(s)
	return nil
}

// decodeEnvelope reads an answer; a malformed one (not a JSON object, a
// ReturnCode of the wrong type or missing) is an error.
func decodeEnvelope(raw []byte) (*envelope, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, api.NewError(api.CodeServerError, "eway: malformed answer")
	}
	if env.ReturnCode == "" {
		return nil, api.NewError(api.CodeServerError, "eway: answer without a return code")
	}
	return &env, nil
}

// rcClass is the return code fit for a log line or an error: ASCII
// letters and digits only, at most 64 of them.
func rcClass(rc string) string {
	var b strings.Builder
	for _, r := range rc {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			if b.Len() >= 64 {
				break
			}
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// wireItem is the subset of an item's fields this client reads; every
// field tolerant of null.
type wireItem struct {
	ItemGUID              flexString     `json:"ItemGUID"`
	FileAs                flexString     `json:"FileAs"`
	Email                 flexString     `json:"Email"`
	Email1Address         flexString     `json:"Email1Address"`
	Email2Address         flexString     `json:"Email2Address"`
	Email3Address         flexString     `json:"Email3Address"`
	TrackedDomains        flexString     `json:"TrackedDomains"`
	CompaniesCompanyGUID  flexString     `json:"Companies_CompanyGuid"`
	IsCompleted           *bool          `json:"IsCompleted"`
	IsLost                *bool          `json:"IsLost"`
	IsPrivate             *bool          `json:"IsPrivate"`
	ItemChanged           flexString     `json:"ItemChanged"`
	ItemCreated           flexString     `json:"ItemCreated"`
	SenderEmailAddress    flexString     `json:"SenderEmailAddress"`
	SentOn                flexString     `json:"SentOn"`
	LeadsOutlookProject   flexString     `json:"Leads_OutlookProjectGuid"`
	ProjectsOutlookProjec flexString     `json:"Projects_OutlookProjectGuid"`
	Relations             []wireRelation `json:"Relations"`
}

type wireRelation struct {
	ForeignFolderName flexString `json:"ForeignFolderName"`
	RelationType      flexString `json:"RelationType"`
	ForeignItemGUID   flexString `json:"ForeignItemGUID"`
	RelationDataGUID  flexString `json:"RelationDataGUID"`
}

// maxItemsPerAnswer caps the items read from one answer.
const maxItemsPerAnswer = 1000

// maxRelationsPerItem caps the relations read of one item.
const maxRelationsPerItem = 500

// decodeItems reads Data as a list of items; a Data that is not a list (or
// null) is a malformed answer. Items without a GUID of the right shape are
// skipped.
func decodeItems(raw json.RawMessage) ([]wireItem, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] != '[' {
		return nil, api.NewError(api.CodeServerError, "eway: malformed answer: data is not a list")
	}
	var items []wireItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, api.NewError(api.CodeServerError, "eway: malformed answer: items")
	}
	out := items[:0]
	for _, it := range items {
		if cleanGUID(string(it.ItemGUID)) == "" {
			continue
		}
		out = append(out, it)
		if len(out) >= maxItemsPerAnswer {
			break
		}
	}
	return out, nil
}

// relationsOf cleans an item's relations.
func relationsOf(w wireItem) []Relation {
	var out []Relation
	for _, r := range w.Relations {
		g := cleanGUID(string(r.ForeignItemGUID))
		if g == "" {
			continue
		}
		out = append(out, Relation{
			Kind:         KindOf(string(r.ForeignFolderName)),
			ForeignGUID:  g,
			Type:         strings.ToUpper(rcClass(string(r.RelationType))),
			RelationData: cleanGUID(string(r.RelationDataGUID)),
		})
		if len(out) >= maxRelationsPerItem {
			break
		}
	}
	return out
}

// parseTime reads eWay-CRM's item times ("2026-10-17 18:07:58", ISO 8601
// with or without a zone); zero when it cannot.
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04:05.999999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// itemOf cleans a wire item of the kind.
func itemOf(kind api.CrmItemKind, w wireItem) Item {
	it := Item{Ref: Ref{Kind: kind, GUID: cleanGUID(string(w.ItemGUID))}, Name: cleanName(string(w.FileAs)), Open: true}
	for _, e := range []flexString{w.Email1Address, w.Email, w.Email2Address, w.Email3Address} {
		if a := CleanEmail(string(e)); a != "" {
			it.Email = a
			break
		}
	}
	it.CompanyGUID = cleanGUID(string(w.CompaniesCompanyGUID))
	if kind == api.CrmDeal || kind == api.CrmProject {
		it.Open = !(w.IsCompleted != nil && *w.IsCompleted) && !(w.IsLost != nil && *w.IsLost)
	}
	it.Changed = parseTime(string(w.ItemChanged))
	if it.Changed.IsZero() {
		it.Changed = parseTime(string(w.ItemCreated))
	}
	it.Relations = relationsOf(w)
	it.Detail = cleanDetail(it.Email)
	return it
}

// ---------------------------------------------------------------------------
// The typed query API (Query): WCF's JSON needs "__type" as the first key
// of every typed object, so these are structs with that field first (a
// map would be encoded with sorted keys).
// ---------------------------------------------------------------------------

type queryTable struct {
	Type string `json:"__type"`
}

type queryColumn struct {
	Type   string     `json:"__type"`
	Source queryTable `json:"Source"`
	Name   string     `json:"Name"`
}

type queryLike struct {
	Type  string      `json:"__type"`
	Field queryColumn `json:"Field"`
	Value string      `json:"Value"`
}

type queryPaging struct {
	Skip int `json:"Skip"`
	Take int `json:"Take"`
}

type mainTableQuery struct {
	Type      string        `json:"__type"`
	ItemTypes []string      `json:"ItemTypes"`
	Paging    queryPaging   `json:"Paging"`
	Fields    []queryColumn `json:"Fields"`
	Filter    queryLike     `json:"Filter"`
}

func column(name string) queryColumn {
	return queryColumn{Type: "Column:#EQ", Source: queryTable{Type: "MainTable:#EQ"}, Name: name}
}

// likeQuery is a query of folder's items whose column is LIKE pattern,
// returning fields, at most take rows.
func likeQuery(folder, col, pattern string, take int, fields ...string) mainTableQuery {
	q := mainTableQuery{
		Type:      "MainTableQuery:#EQ",
		ItemTypes: []string{folder},
		Paging:    queryPaging{Skip: 0, Take: take},
		Filter:    queryLike{Type: "LikeFilterExpressionPredicate:#EQ", Field: column(col), Value: pattern},
	}
	for _, f := range fields {
		q.Fields = append(q.Fields, column(f))
	}
	return q
}
