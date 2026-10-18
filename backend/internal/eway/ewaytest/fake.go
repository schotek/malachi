// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package ewaytest is a fake eWay-CRM web service for tests: the subset of
// API.svc that internal/eway uses, with the behaviour verified against
// eWay's public trial (docs/eway.md §0.3 and "As verified (T0)"). No
// network: an httptest TLS server.
package ewaytest

import (
	"bytes"
	"crypto/md5" //nolint:gosec // the service's LogIn hash
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// Item is a contact, company, lead (deal) or project of the fake.
type Item struct {
	GUID           string
	FileAs         string
	Email          string // companies
	Email1, Email2 string // contacts
	Email3         string
	TrackedDomains string // companies
	CompanyGUID    string // contacts
	IsCompleted    bool
	IsLost         bool
	Changed        time.Time
}

// Email is a saved e-mail item.
type Email struct {
	GUID, FileAs, Subject, Sender, To, Cc, SentOn, MessageID, Note string
	Private                                                        bool
	Attachments                                                    int
	LeadFK, ProjectFK                                              string
	Hash                                                           string
	Created                                                        time.Time
	Binary                                                         []byte
}

// Relation is one relation of the fake.
type Relation struct {
	DataGUID       string
	GUID1, Folder1 string
	GUID2, Folder2 string
	Type           string
}

// Server is the fake. Its fields may be set before the first request;
// later through the methods, which lock.
type Server struct {
	*httptest.Server

	// Path is the instance's path ("/acme"); ServiceURL = URL + Path.
	Path               string
	Username, Password string
	// AzureAuth answers GetServiceAuthSettings with IsAzureAuth true;
	// NoAuthSettings answers it like an older server (404 HTML page).
	AzureAuth, NoAuthSettings bool
	// LoginCode, when set, is LogIn's return code for the right password.
	LoginCode string
	// IdleTimeout ends a session idle that long (0 = 20 minutes).
	IdleTimeout time.Duration
	// UploadLimit refuses larger uploads with 413 (0 = none).
	UploadLimit int64

	mu        sync.Mutex
	now       func() time.Time
	sessions  map[string]time.Time
	userGUID  string
	userEmail string
	binaries  map[string][]byte
	emails    map[string]*Email
	items     map[string]map[string]*Item // folder → guid → item
	relations []*Relation
	calls     []string
	logins    int
	throttle  int
	override  map[string][]byte
	redirect  string
	nullFKs   int
}

// NullForeignKeys counts the SaveEmail updates that set a superior foreign
// key to null.
func (f *Server) NullForeignKeys() int { f.mu.Lock(); defer f.mu.Unlock(); return f.nullFKs }

// New starts a fake with one user (user / secret) at Path "/acme".
func New() *Server {
	f := &Server{
		Path: "/acme", Username: "user", Password: "secret",
		now:      time.Now,
		sessions: map[string]time.Time{},
		userGUID: NewGUID(), userEmail: "user@example.org",
		binaries: map[string][]byte{},
		emails:   map[string]*Email{},
		items: map[string]map[string]*Item{
			"Contacts": {}, "Companies": {}, "Leads": {}, "Projects": {},
		},
		override: map[string][]byte{},
	}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	return f
}

// ServiceURL is the instance's address as a user types it.
func (f *Server) ServiceURL() string { return f.URL + f.Path }

// SetNow replaces the fake's clock (session idle time).
func (f *Server) SetNow(now func() time.Time) { f.mu.Lock(); f.now = now; f.mu.Unlock() }

// SetUserEmail sets the signed-in user's Email1Address ("" = the user
// has none, as the trial's api user and some real users).
func (f *Server) SetUserEmail(email string) { f.mu.Lock(); f.userEmail = email; f.mu.Unlock() }

// NewGUID is a random lower-case GUID.
func NewGUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Add stores an item in a folder ("Contacts", "Companies", "Leads",
// "Projects") and returns its GUID.
func (f *Server) Add(folder string, it Item) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if it.GUID == "" {
		it.GUID = NewGUID()
	}
	cp := it
	f.items[folder][strings.ToLower(it.GUID)] = &cp
	return it.GUID
}

// Relate stores a relation (as SaveRelation would).
func (f *Server) Relate(guid1, folder1, guid2, folder2, typ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.relations = append(f.relations, &Relation{DataGUID: NewGUID(), GUID1: guid1, Folder1: folder1, GUID2: guid2, Folder2: folder2, Type: typ})
}

// Emails returns copies of the saved e-mails.
func (f *Server) Emails() []Email {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Email
	for _, e := range f.emails {
		out = append(out, *e)
	}
	return out
}

// AddEmail stores an e-mail item directly (one saved by someone else).
func (f *Server) AddEmail(e Email) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e.GUID == "" {
		e.GUID = NewGUID()
	}
	if e.Created.IsZero() {
		e.Created = f.now()
	}
	cp := e
	f.emails[strings.ToLower(e.GUID)] = &cp
	return e.GUID
}

// DropEmail deletes an e-mail item as another client would.
func (f *Server) DropEmail(guid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteEmail(strings.ToLower(guid))
}

// Relations returns copies of the relations naming guid.
func (f *Server) Relations(guid string) []Relation {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Relation
	for _, r := range f.relations {
		if strings.EqualFold(r.GUID1, guid) || strings.EqualFold(r.GUID2, guid) {
			out = append(out, *r)
		}
	}
	return out
}

// Binaries is the number of uploaded binaries (orphans included).
func (f *Server) Binaries() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.binaries) }

// Calls returns the methods called so far, in order.
func (f *Server) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Logins counts the successful LogIn calls.
func (f *Server) Logins() int { f.mu.Lock(); defer f.mu.Unlock(); return f.logins }

// ExpireSessions ends every session (the next call answers rcBadSession).
func (f *Server) ExpireSessions() {
	f.mu.Lock()
	f.sessions = map[string]time.Time{}
	f.mu.Unlock()
}

// Throttle answers the next n requests 429 with Retry-After: 1.
func (f *Server) Throttle(n int) { f.mu.Lock(); f.throttle = n; f.mu.Unlock() }

// Override answers every call of method with raw (a JSON answer), session
// checks skipped; nil removes it.
func (f *Server) Override(method string, raw []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if raw == nil {
		delete(f.override, method)
		return
	}
	f.override[method] = raw
}

// RedirectNext answers the next request with a 302 to location.
func (f *Server) RedirectNext(location string) { f.mu.Lock(); f.redirect = location; f.mu.Unlock() }

// --- serving ---------------------------------------------------------------

type answer map[string]any

func (f *Server) serve(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.UserAgent(), "MalachiMail") {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "error code: 1010")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.redirect != "" {
		loc := f.redirect
		f.redirect = ""
		http.Redirect(w, r, loc, http.StatusFound)
		return
	}
	if f.throttle > 0 {
		f.throttle--
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	prefix := f.Path + "/API.svc/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		f.htmlPage(w, http.StatusNotFound)
		return
	}
	method := strings.TrimPrefix(r.URL.Path, prefix)
	f.calls = append(f.calls, method)
	if raw, ok := f.override[method]; ok {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
		return
	}
	if method == "GetServiceAuthSettings" {
		switch {
		case f.NoAuthSettings:
			f.htmlPage(w, http.StatusNotFound)
		case r.Method != http.MethodGet:
			f.htmlPage(w, http.StatusMethodNotAllowed)
		default:
			f.json(w, answer{"ReturnCode": "rcSuccess", "Description": nil, "IsActiveDirectory": false, "IsAzureAuth": f.AzureAuth})
		}
		return
	}
	if r.Method != http.MethodPost {
		f.htmlPage(w, http.StatusMethodNotAllowed)
		return
	}
	if method == "SaveBinaryAttachment" {
		f.upload(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		f.htmlPage(w, http.StatusBadRequest)
		return
	}
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		f.json(w, answer{"ReturnCode": "rcParameterError", "Description": "Bad JSON."})
		return
	}
	if method == "LogIn" {
		f.logIn(w, req)
		return
	}
	var sid string
	_ = json.Unmarshal(req["sessionId"], &sid)
	if !f.session(sid) {
		f.json(w, answer{"ReturnCode": "rcBadSession", "Description": "Web service returned an error (rcBadSession): Invalid SID '" + sid + "' supplied.", "Data": nil})
		return
	}
	h, ok := handlers[method]
	if !ok {
		f.htmlPage(w, http.StatusNotFound)
		return
	}
	f.json(w, h(f, req, body))
}

func (f *Server) htmlPage(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "\xef\xbb\xbf<?xml version=\"1.0\"?><!DOCTYPE html><html><head><title>Service</title></head><body>Service</body></html>")
}

func (f *Server) json(w http.ResponseWriter, a answer) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(a)
}

func (f *Server) session(sid string) bool {
	last, ok := f.sessions[sid]
	idle := f.IdleTimeout
	if idle == 0 {
		idle = 20 * time.Minute
	}
	if !ok || f.now().Sub(last) > idle {
		delete(f.sessions, sid)
		return false
	}
	f.sessions[sid] = f.now()
	return true
}

func (f *Server) logIn(w http.ResponseWriter, req map[string]json.RawMessage) {
	var user, hash, app string
	_ = json.Unmarshal(req["userName"], &user)
	_ = json.Unmarshal(req["passwordHash"], &hash)
	_ = json.Unmarshal(req["appVersion"], &app)
	sum := md5.Sum([]byte(f.Password)) //nolint:gosec // the service's protocol
	if user != f.Username || hash != hex.EncodeToString(sum[:]) || app == "" {
		f.json(w, answer{"ReturnCode": "rcLoginFailed", "Description": "Invalid password or username '" + user + "'", "SessionId": nil, "WcfVersion": "10.0.0.84"})
		return
	}
	if f.LoginCode != "" {
		f.json(w, answer{"ReturnCode": f.LoginCode, "Description": "refused", "SessionId": nil})
		return
	}
	sid := NewGUID()
	f.sessions[sid] = f.now()
	f.logins++
	f.json(w, answer{"ReturnCode": "rcSuccess", "Description": nil, "SessionId": sid, "UserItemGuid": f.userGUID,
		"WcfVersion": "10.0.0.84", "OutlookClientVersion": "10.0.0.82", "IsAdmin": false, "Debug": false})
}

func (f *Server) upload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !f.session(q.Get("sessionId")) {
		f.json(w, answer{"ReturnCode": "rcBadSession", "Description": "Invalid SID '" + q.Get("sessionId") + "' supplied."})
		return
	}
	if r.Header.Get("Content-Type") != "application/octet-stream" {
		f.json(w, answer{"ReturnCode": "rcParameterError", "Description": "Expected a binary."})
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		f.htmlPage(w, http.StatusBadRequest)
		return
	}
	if f.UploadLimit > 0 && int64(len(data)) > f.UploadLimit {
		f.htmlPage(w, http.StatusRequestEntityTooLarge)
		return
	}
	guid := strings.ToLower(q.Get("itemGuid"))
	if guid == "" || q.Get("fileName") == "" {
		f.json(w, answer{"ReturnCode": "rcParameterError", "Description": "Missing parameters."})
		return
	}
	f.binaries[guid] = data
	f.json(w, answer{"ReturnCode": "rcSuccess", "Description": nil})
}

type handler func(f *Server, req map[string]json.RawMessage, raw []byte) answer

var handlers = map[string]handler{
	"LogOut":               (*Server).logOut,
	"GetUsersByItemGuids":  (*Server).getUsers,
	"SaveEmail":            (*Server).saveEmail,
	"SearchEmails":         (*Server).searchEmails,
	"GetEmailsByItemGuids": (*Server).getEmails,
	"DeleteEmail":          (*Server).deleteEmailCall,
	"SaveRelation":         (*Server).saveRelation,
	"DeleteRelation":       (*Server).deleteRelation,
	"SearchContacts":       (*Server).searchContacts,
	"SearchCompanies":      (*Server).searchCompanies,
	"Query":                (*Server).query,
	"GetContactsByItemGuids": func(f *Server, req map[string]json.RawMessage, _ []byte) answer {
		return f.getItems("Contacts", req)
	},
	"GetCompaniesByItemGuids": func(f *Server, req map[string]json.RawMessage, _ []byte) answer {
		return f.getItems("Companies", req)
	},
	"GetLeadsByItemGuids": func(f *Server, req map[string]json.RawMessage, _ []byte) answer {
		return f.getItems("Leads", req)
	},
	"GetProjectsByItemGuids": func(f *Server, req map[string]json.RawMessage, _ []byte) answer {
		return f.getItems("Projects", req)
	},
}

func ok(data any) answer { return answer{"ReturnCode": "rcSuccess", "Description": nil, "Data": data} }

func fail(rc, desc string) answer { return answer{"ReturnCode": rc, "Description": desc} }

func (f *Server) logOut(req map[string]json.RawMessage, _ []byte) answer {
	var sid string
	_ = json.Unmarshal(req["sessionId"], &sid)
	delete(f.sessions, sid)
	return answer{"ReturnCode": "rcSuccess", "Description": nil}
}

func (f *Server) getUsers(req map[string]json.RawMessage, _ []byte) answer {
	var guids []string
	_ = json.Unmarshal(req["itemGuids"], &guids)
	var out []answer
	for _, g := range guids {
		if strings.EqualFold(g, f.userGUID) {
			out = append(out, answer{"ItemGUID": f.userGUID, "Username": f.Username, "Email1Address": f.userEmail,
				"Email2Address": nil, "FileAs": "User, Test", "Server_Password": "never-log-me"})
		}
	}
	return ok(out)
}

func transmit(req map[string]json.RawMessage) map[string]json.RawMessage {
	var t map[string]json.RawMessage
	_ = json.Unmarshal(req["transmitObject"], &t)
	return t
}

func str(t map[string]json.RawMessage, k string) (string, bool) {
	raw, ok := t[k]
	if !ok || string(raw) == "null" {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func (f *Server) saveEmail(req map[string]json.RawMessage, _ []byte) answer {
	t := transmit(req)
	guid, _ := str(t, "ItemGUID")
	guid = strings.ToLower(guid)
	if guid == "" {
		return fail("rcParameterError", "ItemGUID required.")
	}
	if e, ok := f.emails[guid]; ok { // an update of the given fields
		if raw, has := t["IsPrivate"]; has {
			_ = json.Unmarshal(raw, &e.Private)
		}
		for _, fk := range []struct{ key, folder string }{{"Leads_OutlookProjectGuid", "Leads"}, {"Projects_OutlookProjectGuid", "Projects"}} {
			raw, has := t[fk.key]
			switch {
			case !has:
			case string(raw) == "null":
				// null clears this key (and its relation) only.
				if (fk.folder == "Leads" && e.LeadFK != "") || (fk.folder == "Projects" && e.ProjectFK != "") {
					f.setSuperior(e, fk.folder, "")
				}
				f.nullFKs++
			default:
				v, _ := str(t, fk.key)
				f.setSuperior(e, fk.folder, v)
			}
		}
		return answer{"ReturnCode": "rcSuccess", "Guid": e.GUID}
	}
	bin, ok := f.binaries[guid]
	if !ok {
		return fail("rcGeneralError", "The e-mail's file was not uploaded.")
	}
	if _, has := t["AttachmentsCount"]; !has || string(t["AttachmentsCount"]) == "null" {
		return fail("rcDatabaseError", "Cannot insert the value NULL into column 'AttachmentsCount'.")
	}
	e := &Email{GUID: guid, Binary: bin, Created: f.now()}
	e.FileAs, _ = str(t, "FileAs")
	e.Subject, _ = str(t, "Subject")
	e.Sender, _ = str(t, "SenderEmailAddress")
	e.To, _ = str(t, "To")
	e.Cc, _ = str(t, "Cc")
	e.SentOn, _ = str(t, "SentOn")
	e.MessageID, _ = str(t, "MessageId")
	e.Note, _ = str(t, "Note")
	_ = json.Unmarshal(t["AttachmentsCount"], &e.Attachments)
	if raw, has := t["IsPrivate"]; has {
		_ = json.Unmarshal(raw, &e.Private)
	}
	quartet := []string{e.FileAs, e.Sender, e.To, e.SentOn}
	set := 0
	for _, v := range quartet {
		if v != "" {
			set++
		}
	}
	switch set {
	case 0:
	case 4:
		e.Hash = strings.ToLower(strings.Join(quartet, "|"))
		for _, other := range f.emails {
			if other.Hash == e.Hash {
				return fail("rcDuplicateEmail", "The e-mail already exists.")
			}
		}
	default:
		return fail("rcGeneralError", "FileAs, SenderEmailAddress, To and SentOn are needed for the hash.")
	}
	f.emails[guid] = e
	if v, has := str(t, "Leads_OutlookProjectGuid"); has && v != "" {
		f.setSuperior(e, "Leads", v)
	}
	if v, has := str(t, "Projects_OutlookProjectGuid"); has && v != "" {
		f.setSuperior(e, "Projects", v)
	}
	return answer{"ReturnCode": "rcSuccess", "Guid": e.GUID}
}

// setSuperior sets one foreign key, clears the other and keeps the one
// OUTLOOKPROJECT relation in step (T0 4).
func (f *Server) setSuperior(e *Email, folder, guid string) {
	guid = strings.ToLower(guid)
	e.LeadFK, e.ProjectFK = "", ""
	kept := f.relations[:0]
	for _, r := range f.relations {
		if !(r.Type == "OUTLOOKPROJECT" && strings.EqualFold(r.GUID1, e.GUID)) {
			kept = append(kept, r)
		}
	}
	f.relations = kept
	if guid == "" {
		return
	}
	if folder == "Leads" {
		e.LeadFK = guid
	} else {
		e.ProjectFK = guid
	}
	f.relations = append(f.relations, &Relation{DataGUID: NewGUID(), GUID1: e.GUID, Folder1: "Emails", GUID2: guid, Folder2: folder, Type: "OUTLOOKPROJECT"})
}

func (f *Server) searchEmails(req map[string]json.RawMessage, _ []byte) answer {
	t := transmit(req)
	mid, _ := str(t, "MessageId")
	var out []answer
	for _, e := range f.emails {
		if mid != "" && strings.EqualFold(e.MessageID, mid) {
			out = append(out, f.emailAnswer(e, false))
		}
	}
	return ok(out)
}

func (f *Server) emailAnswer(e *Email, relations bool) answer {
	a := answer{"ItemGUID": e.GUID, "FileAs": e.FileAs, "Subject": e.Subject, "MessageId": e.MessageID,
		"SenderEmailAddress": nilIfEmpty(e.Sender), "SentOn": nilIfEmpty(e.SentOn),
		"IsPrivate": e.Private, "ItemCreated": e.Created.UTC().Format("2006-01-02 15:04:05"),
		"Leads_OutlookProjectGuid": nilIfEmpty(e.LeadFK), "Projects_OutlookProjectGuid": nilIfEmpty(e.ProjectFK)}
	if relations {
		a["Relations"] = f.relationsOf(e.GUID)
	}
	return a
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (f *Server) relationsOf(guid string) []answer {
	out := []answer{}
	for _, r := range f.relations {
		switch {
		case strings.EqualFold(r.GUID1, guid):
			out = append(out, answer{"ForeignFolderName": r.Folder2, "RelationType": r.Type, "ForeignItemGUID": r.GUID2, "RelationDataGUID": r.DataGUID, "ItemGUID": NewGUID()})
		case strings.EqualFold(r.GUID2, guid):
			out = append(out, answer{"ForeignFolderName": r.Folder1, "RelationType": r.Type, "ForeignItemGUID": r.GUID1, "RelationDataGUID": r.DataGUID, "ItemGUID": NewGUID()})
		}
	}
	return out
}

func flag(req map[string]json.RawMessage, k string) bool {
	var b bool
	_ = json.Unmarshal(req[k], &b)
	return b
}

func (f *Server) getEmails(req map[string]json.RawMessage, _ []byte) answer {
	var guids []string
	_ = json.Unmarshal(req["itemGuids"], &guids)
	var out []answer
	for _, g := range guids {
		if e, ok := f.emails[strings.ToLower(g)]; ok {
			out = append(out, f.emailAnswer(e, flag(req, "includeRelations")))
		}
	}
	return ok(out)
}

func (f *Server) deleteEmailCall(req map[string]json.RawMessage, _ []byte) answer {
	var g string
	_ = json.Unmarshal(req["itemGuid"], &g)
	f.deleteEmail(strings.ToLower(g))
	return answer{"ReturnCode": "rcSuccess", "Description": nil}
}

func (f *Server) deleteEmail(g string) {
	delete(f.emails, g)
	kept := f.relations[:0]
	for _, r := range f.relations {
		if !strings.EqualFold(r.GUID1, g) && !strings.EqualFold(r.GUID2, g) {
			kept = append(kept, r)
		}
	}
	f.relations = kept
}

func (f *Server) exists(folder, guid string) bool {
	if folder == "Emails" {
		_, ok := f.emails[strings.ToLower(guid)]
		return ok
	}
	m, ok := f.items[folder]
	if !ok {
		return false
	}
	_, ok = m[strings.ToLower(guid)]
	return ok
}

func (f *Server) saveRelation(req map[string]json.RawMessage, _ []byte) answer {
	t := transmit(req)
	g1, _ := str(t, "ItemGUID1")
	f1, _ := str(t, "FolderName1")
	g2, _ := str(t, "ItemGUID2")
	f2, _ := str(t, "FolderName2")
	typ, _ := str(t, "RelationType")
	if !f.exists(f1, g1) || !f.exists(f2, g2) {
		return answer{"ReturnCode": "rcGeneralError", "Description": "Item not found.", "Result": false}
	}
	if typ == "OUTLOOKPROJECT" {
		if f1 != "Emails" {
			return answer{"ReturnCode": "rcGeneralError", "Description": "Relation between modules 'Emails' and '" + f1 + "' with RelationType 'OUTLOOKPROJECT' requires Direction to be set.", "Result": false}
		}
		for _, r := range f.relations {
			if r.Type == "OUTLOOKPROJECT" && strings.EqualFold(r.GUID1, g1) {
				return answer{"ReturnCode": "rcDuplicateRelation", "Description": "Cannot insert duplicate key row.", "Result": false}
			}
		}
		e := f.emails[strings.ToLower(g1)]
		f.setSuperior(e, f2, g2)
		return answer{"ReturnCode": "rcSuccess", "Result": true}
	}
	for _, r := range f.relations {
		if r.Type == typ && ((strings.EqualFold(r.GUID1, g1) && strings.EqualFold(r.GUID2, g2)) || (strings.EqualFold(r.GUID1, g2) && strings.EqualFold(r.GUID2, g1))) {
			return answer{"ReturnCode": "rcDuplicateRelation", "Description": "Cannot insert duplicate key row.", "Result": false}
		}
	}
	f.relations = append(f.relations, &Relation{DataGUID: NewGUID(), GUID1: strings.ToLower(g1), Folder1: f1, GUID2: strings.ToLower(g2), Folder2: f2, Type: typ})
	return answer{"ReturnCode": "rcSuccess", "Result": true}
}

func (f *Server) deleteRelation(req map[string]json.RawMessage, _ []byte) answer {
	var g string
	if json.Unmarshal(req["relationDataGuid"], &g) != nil || g == "" {
		return fail("rcParameterError", "Missing relationDataGuid parameter of the item that has to be deleted.")
	}
	kept := f.relations[:0]
	for _, r := range f.relations {
		if strings.EqualFold(r.DataGUID, g) {
			// The e-mail's foreign key stays: whether the real service
			// clears it with the relation is not known, so the client
			// must clear it itself.
			continue
		}
		kept = append(kept, r)
	}
	f.relations = kept
	return answer{"ReturnCode": "rcSuccess", "Description": nil}
}

func (f *Server) itemAnswer(folder string, it *Item, relations, fks bool) answer {
	a := answer{"ItemGUID": it.GUID, "FileAs": it.FileAs, "ItemChanged": it.Changed.UTC().Format("2006-01-02 15:04:05")}
	switch folder {
	case "Contacts":
		a["Email1Address"], a["Email2Address"], a["Email3Address"] = it.Email1, nilIfEmpty(it.Email2), nilIfEmpty(it.Email3)
		if fks {
			a["Companies_CompanyGuid"] = nilIfEmpty(it.CompanyGUID)
		}
	case "Companies":
		a["Email"], a["TrackedDomains"] = it.Email, nilIfEmpty(it.TrackedDomains)
	default:
		a["IsCompleted"], a["IsLost"], a["StateEn"] = it.IsCompleted, it.IsLost, nil
	}
	if relations {
		a["Relations"] = f.relationsOf(it.GUID)
	}
	return a
}

func (f *Server) getItems(folder string, req map[string]json.RawMessage) answer {
	var guids []string
	_ = json.Unmarshal(req["itemGuids"], &guids)
	var out []answer
	for _, g := range guids {
		if it, ok := f.items[folder][strings.ToLower(g)]; ok {
			out = append(out, f.itemAnswer(folder, it, flag(req, "includeRelations"), flag(req, "includeForeignKeys")))
		}
	}
	return ok(out)
}

func (f *Server) searchContacts(req map[string]json.RawMessage, _ []byte) answer {
	t := transmit(req)
	var op string
	_ = json.Unmarshal(req["binaryLogicalOperator"], &op)
	e1, _ := str(t, "Email1Address")
	e2, _ := str(t, "Email2Address")
	e3, _ := str(t, "Email3Address")
	var out []answer
	for _, it := range f.items["Contacts"] {
		m1, m2, m3 := e1 != "" && strings.EqualFold(it.Email1, e1), e2 != "" && strings.EqualFold(it.Email2, e2), e3 != "" && strings.EqualFold(it.Email3, e3)
		hit := m1 && (e2 == "" || m2) && (e3 == "" || m3)
		if op == "OR" {
			hit = m1 || m2 || m3
		}
		if hit {
			out = append(out, f.itemAnswer("Contacts", it, false, false)) // search results carry no FKs (T0 6)
		}
	}
	return ok(out)
}

func (f *Server) searchCompanies(req map[string]json.RawMessage, _ []byte) answer {
	t := transmit(req)
	email, _ := str(t, "Email")
	domains, _ := str(t, "TrackedDomains")
	var out []answer
	for _, it := range f.items["Companies"] {
		if (email != "" && strings.EqualFold(it.Email, email)) || (domains != "" && strings.EqualFold(it.TrackedDomains, domains)) {
			out = append(out, f.itemAnswer("Companies", it, false, false))
		}
	}
	return ok(out)
}

// query is the typed Query with a LikeFilterExpressionPredicate; every
// typed object must have "__type" first, else WCF does not know its type.
func (f *Server) query(req map[string]json.RawMessage, _ []byte) answer {
	if err := typeFirst(req["query"]); err != nil {
		return fail("rcGeneralError", err.Error())
	}
	var q struct {
		Type      string   `json:"__type"`
		ItemTypes []string `json:"ItemTypes"`
		Paging    struct{ Skip, Take int }
		Fields    []struct{ Name string }
		Filter    struct {
			Type  string `json:"__type"`
			Field struct{ Name string }
			Value string
		}
	}
	if err := json.Unmarshal(req["query"], &q); err != nil || q.Type != "MainTableQuery:#EQ" || len(q.ItemTypes) != 1 || q.Filter.Type != "LikeFilterExpressionPredicate:#EQ" {
		return fail("rcGeneralError", "Unsupported query.")
	}
	folder := q.ItemTypes[0]
	items, ok2 := f.items[folder]
	if !ok2 {
		return fail("rcGeneralError", "Unknown item type.")
	}
	needle := strings.ToLower(strings.Trim(q.Filter.Value, "%"))
	var out []answer
	for _, it := range items {
		full := f.itemAnswer(folder, it, false, false)
		v, has := full[q.Filter.Field.Name]
		if !has {
			return fail("rcGeneralError", "Unknown column name '"+q.Filter.Field.Name+"' in 'EWD_"+folder+"'.")
		}
		s, _ := v.(string)
		if !strings.Contains(strings.ToLower(s), needle) {
			continue
		}
		row := answer{}
		for _, fl := range q.Fields {
			row[fl.Name] = full[fl.Name]
		}
		out = append(out, row)
		if q.Paging.Take > 0 && len(out) >= q.Paging.Take {
			break
		}
	}
	return ok(out)
}

// typeFirst checks that every JSON object with a "__type" key has it first.
func typeFirst(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	type frame struct {
		object bool
		keys   int
	}
	var stack []frame
	expectKey := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{':
				stack = append(stack, frame{object: true})
				expectKey = true
				continue
			case '[':
				stack = append(stack, frame{})
			case '}', ']':
				stack = stack[:len(stack)-1]
			}
		case string:
			if expectKey && len(stack) > 0 && stack[len(stack)-1].object {
				top := &stack[len(stack)-1]
				if v == "__type" && top.keys > 0 {
					return fmt.Errorf("__type is not the first member")
				}
				top.keys++
				expectKey = false
				continue
			}
		}
		if len(stack) > 0 && stack[len(stack)-1].object {
			expectKey = true
		}
	}
}
