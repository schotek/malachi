// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/schotek/malachi/backend/internal/eway/ewaytest"
	"github.com/schotek/malachi/backend/internal/jira/jiratest"
)

// password is every user's password.
const password = "test"

// corpusUser is the user a -corpus directory is seeded into (what the
// Windows UI tests' devmail does).
const corpusUser = "test@example.test"

// folders are created for every user; their English names carry the
// roles (internal/imap roleNames), so no SPECIAL-USE is needed.
var folders = []string{"INBOX", "Sent", "Drafts", "Trash", "Junk", "Archive"}

// mailbox is the server's view of its users, for SMTP delivery and the
// control endpoint.
type mailbox struct {
	mu    sync.Mutex
	users map[string]*imapmemserver.User // lower-case address → user
	order []string
}

func (m *mailbox) add(mem *imapmemserver.Server, email string) (*imapmemserver.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := strings.ToLower(email)
	if u, ok := m.users[key]; ok {
		return u, nil
	}
	u := imapmemserver.NewUser(email, password)
	for _, f := range folders {
		if err := u.Create(f, nil); err != nil {
			return nil, fmt.Errorf("create %s for %s: %w", f, email, err)
		}
	}
	mem.AddUser(u)
	m.users[key] = u
	m.order = append(m.order, key)
	return u, nil
}

func (m *mailbox) user(email string) *imapmemserver.User {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.users[strings.ToLower(email)]
}

// literal is an IMAP literal of a message.
type literal struct{ *bytes.Reader }

func (l literal) Size() int64 { return l.Reader.Size() }

func appendRaw(u *imapmemserver.User, folder string, raw []byte, at time.Time, flags []imap.Flag) error {
	_, err := u.Append(folder, literal{bytes.NewReader(raw)}, &imap.AppendOptions{Time: at, Flags: flags})
	return err
}

func imapFlags(names []string) []imap.Flag {
	var out []imap.Flag
	for _, n := range names {
		switch strings.ToLower(n) {
		case "seen":
			out = append(out, imap.FlagSeen)
		case "flagged":
			out = append(out, imap.FlagFlagged)
		case "answered":
			out = append(out, imap.FlagAnswered)
		case "draft":
			out = append(out, imap.FlagDraft)
		}
	}
	return out
}

func serve(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	imapPort := fs.Int("imap-port", 0, "IMAP port on 127.0.0.1 (0 = a free one)")
	smtpPort := fs.Int("smtp-port", 0, "SMTP submission port on 127.0.0.1 (0 = a free one)")
	controlPort := fs.Int("control-port", 0, "HTTP control port on 127.0.0.1 (0 = a free one)")
	lang := fs.String("lang", "en", "the built-in sample set: en or cs")
	corpusDir := fs.String("corpus", "", "a directory of .eml files seeded into the INBOX of "+corpusUser+" instead of the sample set")
	nowFlag := fs.String("now", "", "the sample set's clock, RFC 3339 (default: now)")
	jiraPort := fs.Int("jira-port", 0, "the sample set's Jira Cloud site on 127.0.0.1 (0 = a free one)")
	noJira := fs.Bool("no-jira", false, "no Jira site")
	ewayPort := fs.Int("eway-port", 0, "the sample set's eWay-CRM instance on 127.0.0.1 (0 = a free one)")
	noEway := fs.Bool("no-eway", false, "no eWay-CRM instance")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	now := time.Now()
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			fmt.Fprintf(stderr, "malachi-devmail serve: -now: %v\n", err)
			return 2
		}
		now = t.In(time.Local)
	}

	mem := imapmemserver.New()
	box := &mailbox{users: map[string]*imapmemserver.User{}}
	var user string
	var jira *jiratest.Server
	var jiraPortUsed int
	var eway *ewaytest.Server
	var ewayPortUsed int
	if *corpusDir != "" {
		u, err := box.add(mem, corpusUser)
		if err != nil {
			fmt.Fprintf(stderr, "malachi-devmail serve: %v\n", err)
			return 1
		}
		n, err := seedDirectory(u, *corpusDir)
		if err != nil {
			fmt.Fprintf(stderr, "malachi-devmail serve: %v\n", err)
			return 1
		}
		user = corpusUser
		fmt.Fprintf(stderr, "malachi-devmail: %d messages from %s in the INBOX of %s\n", n, *corpusDir, user)
	} else {
		c, err := loadCorpus(*lang)
		if err != nil {
			fmt.Fprintf(stderr, "malachi-devmail serve: %v\n", err)
			return 1
		}
		built, err := c.build(now)
		if err != nil {
			fmt.Fprintf(stderr, "malachi-devmail serve: %v\n", err)
			return 1
		}
		u, err := box.add(mem, c.Me.Email)
		if err != nil {
			fmt.Fprintf(stderr, "malachi-devmail serve: %v\n", err)
			return 1
		}
		for _, b := range built {
			if err := appendRaw(u, b.Folder, b.Raw, b.Date, imapFlags(b.Flags)); err != nil {
				fmt.Fprintf(stderr, "malachi-devmail serve: append to %s: %v\n", b.Folder, err)
				return 1
			}
		}
		user = c.Me.Email
		fmt.Fprintf(stderr, "malachi-devmail: %d sample messages (%s) in the mailbox of %s\n", len(built), *lang, user)
		if c.Jira != nil && !*noJira {
			jira, jiraPortUsed, err = startJira(c.Jira, *jiraPort, now)
			if err != nil {
				fmt.Fprintf(stderr, "malachi-devmail serve: %v\n", err)
				return 1
			}
			fmt.Fprintf(stderr, "malachi-devmail: %d issues on the Jira site %s (login %s, token %s)\n", len(c.Jira.Issues), jira.Site, jiratest.Login, jiratest.Token)
		}
		if c.Eway != nil && !*noEway {
			eway, ewayPortUsed, err = startEway(c, *ewayPort)
			if err != nil {
				fmt.Fprintf(stderr, "malachi-devmail serve: %v\n", err)
				return 1
			}
			fmt.Fprintf(stderr, "malachi-devmail: eWay-CRM instance %s (user %s, password %s): %d companies, %d contacts, %d deals, %d projects\n",
				eway.ServiceURL(), ewayUser, ewayPassword, len(c.Eway.Companies), len(c.Eway.Contacts), len(c.Eway.Deals), len(c.Eway.Projects))
		}
	}

	imapLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *imapPort))
	if err != nil {
		fmt.Fprintf(stderr, "malachi-devmail serve: imap: %v\n", err)
		return 1
	}
	smtpLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *smtpPort))
	if err != nil {
		fmt.Fprintf(stderr, "malachi-devmail serve: smtp: %v\n", err)
		return 1
	}
	controlLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *controlPort))
	if err != nil {
		fmt.Fprintf(stderr, "malachi-devmail serve: control: %v\n", err)
		return 1
	}

	imapSrv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapMove: {}, imap.CapListStatus: {}},
		InsecureAuth: true,
		Logger:       quiet{},
	})
	go func() { _ = imapSrv.Serve(imapLn) }()

	smtpSrv := smtp.NewServer(&smtpBackend{box: box})
	smtpSrv.Domain = "devmail.example.test"
	smtpSrv.AllowInsecureAuth = true
	smtpSrv.MaxMessageBytes = 64 << 20
	smtpSrv.MaxRecipients = 100
	smtpSrv.ReadTimeout = 2 * time.Minute
	smtpSrv.WriteTimeout = 2 * time.Minute
	smtpSrv.ErrorLog = log.New(io.Discard, "", 0)
	go func() { _ = smtpSrv.Serve(smtpLn) }()

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(box.status())
	})
	controlSrv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = controlSrv.Serve(controlLn) }()

	ports := map[string]any{
		"imap":     imapLn.Addr().(*net.TCPAddr).Port,
		"smtp":     smtpLn.Addr().(*net.TCPAddr).Port,
		"control":  controlLn.Addr().(*net.TCPAddr).Port,
		"user":     user,
		"password": password,
	}
	if jira != nil {
		ports["jira"] = jiraPortUsed
		ports["jiraLogin"] = jiratest.Login
		ports["jiraToken"] = jiratest.Token
	}
	if eway != nil {
		ports["eway"] = ewayPortUsed
		ports["ewayUser"] = ewayUser
		ports["ewayPassword"] = ewayPassword
	}
	line, _ := json.Marshal(ports)
	fmt.Fprintf(stdout, "%s\n", line)
	fmt.Fprintf(stderr, "malachi-devmail: imap 127.0.0.1:%d, smtp 127.0.0.1:%d, control http://127.0.0.1:%d/status; user %s, password %s\n",
		ports["imap"], ports["smtp"], ports["control"], user, password)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	fmt.Fprintln(stderr, "malachi-devmail: stopping")
	_ = imapSrv.Close()
	_ = smtpSrv.Close()
	_ = controlSrv.Close()
	if jira != nil {
		jira.Close()
	}
	if eway != nil {
		eway.Close()
	}
	return 0
}

// seedDirectory appends every .eml of dir to the user's INBOX, in name
// order, with the file's modification time as the internal date.
func seedDirectory(u *imapmemserver.User, dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".eml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	n := 0
	for _, name := range names {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return n, err
		}
		at := time.Now()
		if fi, err := os.Stat(path); err == nil {
			at = fi.ModTime()
		}
		if err := appendRaw(u, "INBOX", raw, at, nil); err != nil {
			return n, fmt.Errorf("%s: %w", name, err)
		}
		n++
	}
	return n, nil
}

// status is the control endpoint's answer: every user, every folder, its
// message count.
func (m *mailbox) status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	var users []map[string]any
	for _, key := range m.order {
		u := m.users[key]
		var fl []map[string]any
		for _, f := range folders {
			var total uint32
			if st, err := u.Status(f, &imap.StatusOptions{NumMessages: true}); err == nil && st.NumMessages != nil {
				total = *st.NumMessages
			}
			fl = append(fl, map[string]any{"folder": f, "total": total})
		}
		users = append(users, map[string]any{"user": key, "folders": fl})
	}
	return map[string]any{"users": users}
}

// quiet drops the IMAP server's log (connections come and go while the
// daemon syncs; a person reads the control endpoint instead).
type quiet struct{}

func (quiet) Printf(string, ...any) {}

// smtpBackend accepts submissions from any authenticated user and delivers
// mail addressed to a user of the server into their INBOX.
type smtpBackend struct{ box *mailbox }

func (b *smtpBackend) NewSession(*smtp.Conn) (smtp.Session, error) {
	return &smtpSession{box: b.box}, nil
}

type smtpSession struct {
	box    *mailbox
	authed bool
	rcpts  []string
}

func (s *smtpSession) AuthMechanisms() []string { return []string{sasl.Plain} }

func (s *smtpSession) Auth(mech string) (sasl.Server, error) {
	if mech != sasl.Plain {
		return nil, smtp.ErrAuthUnsupported
	}
	return sasl.NewPlainServer(func(identity, username, pw string) error {
		if s.box.user(username) == nil || pw != password {
			return errors.New("bad user name or password")
		}
		s.authed = true
		return nil
	}), nil
}

func (s *smtpSession) Mail(from string, opts *smtp.MailOptions) error {
	if !s.authed {
		return smtp.ErrAuthRequired
	}
	s.rcpts = nil
	return nil
}

func (s *smtpSession) Rcpt(to string, opts *smtp.RcptOptions) error {
	s.rcpts = append(s.rcpts, to)
	return nil
}

func (s *smtpSession) Data(r io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return err
	}
	for _, to := range s.rcpts {
		if u := s.box.user(to); u != nil {
			if err := appendRaw(u, "INBOX", raw, time.Now(), nil); err != nil {
				return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "delivery failed: " + err.Error()}
			}
		}
	}
	return nil
}

func (s *smtpSession) Reset()        { s.rcpts = nil }
func (s *smtpSession) Logout() error { return nil }
