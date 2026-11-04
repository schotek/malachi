// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/jira/jiratest"
	"github.com/schotek/malachi/backend/pkg/api"
)

// maxLine caps one line from the daemon.
const maxLine = 32 << 20

func defaultSocket() string {
	if p := os.Getenv("MALACHI_SOCKET"); p != "" {
		return p
	}
	if base := api.SocketBase(os.Getenv("XDG_RUNTIME_DIR"), os.Getenv("FLATPAK_ID")); base != "" {
		return filepath.Join(base, api.SocketRelPath)
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		cache = filepath.Join(home, ".cache")
	}
	return filepath.Join(cache, "malachi", "run", "rpc.sock")
}

// addAccount adds the sample user's account to a running daemon.
func addAccount(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("add-account", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", defaultSocket(), "the daemon's socket")
	imapPort := fs.Int("imap-port", 0, "serve's IMAP port")
	smtpPort := fs.Int("smtp-port", 0, "serve's SMTP port")
	jiraPort := fs.Int("jira-port", 0, "serve's Jira port: also adds the Jira account")
	ewayPort := fs.Int("eway-port", 0, "serve's eWay-CRM port: also adds the CRM account and saves the sample set's saved messages")
	lang := fs.String("lang", "en", "the sample set whose user to add (its name and address)")
	email := fs.String("email", "", "the address instead of the sample set's (e.g. "+corpusUser+" for a -corpus server)")
	name := fs.String("name", "", "the account's name instead of the sample user's")
	timeout := fs.Duration("timeout", 45*time.Second, "")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *imapPort == 0 || *smtpPort == 0 {
		fmt.Fprintln(stderr, "malachi-devmail add-account: -imap-port and -smtp-port are required (serve prints them)")
		return 2
	}
	user, display := *email, *name
	var c *Corpus
	if user == "" || display == "" || *ewayPort != 0 {
		var err error
		if c, err = loadCorpus(*lang); err != nil {
			fmt.Fprintf(stderr, "malachi-devmail add-account: %v\n", err)
			return 2
		}
		if user == "" {
			user = c.Me.Email
		}
		if display == "" {
			display = c.Me.Name
		}
	}
	server := func(port int) *api.ServerConfig {
		return &api.ServerConfig{Host: "127.0.0.1", Port: port, Security: api.SecurityNone, Username: user, AuthMethod: api.AuthPassword}
	}
	accounts := []map[string]any{{
		"config":      api.AccountConfig{Name: display, Email: user, IMAP: server(*imapPort), SMTP: server(*smtpPort)},
		"credentials": api.Credentials{Password: password},
	}}
	if *jiraPort != 0 {
		accounts = append(accounts, map[string]any{
			"config": api.AccountConfig{Name: "Acme Jira", Email: jiratest.Login, Kind: api.AccountJira, Jira: &api.JiraConfig{
				SiteURL: fmt.Sprintf("http://127.0.0.1:%d", *jiraPort), Deployment: api.JiraCloud, CloudID: jiratest.CloudID, Login: jiratest.Login,
				Spaces: []api.SpaceRef{{ID: "10000", Key: "ITSD"}, {ID: "10001", Key: "WEB"}, {ID: "10002", Key: "MOB"}},
			}},
			"credentials": api.Credentials{Password: jiratest.Token},
		})
	}
	if *ewayPort != 0 {
		accounts = append(accounts, map[string]any{
			"config": api.AccountConfig{Name: "eWay-CRM", Kind: api.AccountEway, Eway: &api.EwayConfig{
				ServiceURL: fmt.Sprintf("http://127.0.0.1:%d%s", *ewayPort, ewayPath), Username: ewayUser, FollowThreads: true,
			}},
			"credentials": api.Credentials{Password: ewayPassword},
		})
	}
	var ids []string
	for i, acc := range accounts {
		params, _ := json.Marshal(acc)
		result, err := call(*socket, "account.add", params, *timeout, i+1)
		if err != nil {
			fmt.Fprintf(stderr, "malachi-devmail add-account: %v\n", err)
			return 1
		}
		var added struct {
			AccountID string `json:"accountId"`
		}
		_ = json.Unmarshal(result, &added)
		ids = append(ids, added.AccountID)
		fmt.Fprintf(stdout, "%s\n", result)
	}
	if *ewayPort != 0 && c != nil && c.Eway != nil && len(c.Eway.Saved) > 0 {
		if err := saveToCRM(*socket, ids[0], ids[len(ids)-1], c, *timeout, stderr); err != nil {
			fmt.Fprintf(stderr, "malachi-devmail add-account: %v\n", err)
			return 1
		}
	}
	return 0
}

// saveToCRM saves the sample set's saved messages once the mail account
// has synchronised them: each is found in its folder by subject, the
// newest first (a reply shares its subject with the message it answers).
func saveToCRM(socket, mailAccount, crmAccount string, c *Corpus, timeout time.Duration, stderr io.Writer) error {
	for _, sv := range c.Eway.Saved {
		m := c.byID(sv.Message)
		if m == nil {
			return fmt.Errorf("eway: saved message %q is not in the set", sv.Message)
		}
		folder, err := folderNamed(socket, mailAccount, m.Folder, timeout)
		if err != nil {
			return err
		}
		id, err := messageBySubject(socket, mailAccount, folder, m.Subject, timeout)
		if err != nil {
			return err
		}
		var links []api.CrmItemRef
		for _, l := range sv.Link {
			ref, err := ewayRef(c.Eway, l)
			if err != nil {
				return err
			}
			links = append(links, ref)
		}
		params := map[string]any{"accountId": crmAccount, "messageAccountId": mailAccount, "messageIds": []string{id}, "link": links}
		if sv.Superior != "" {
			ref, err := ewayRef(c.Eway, sv.Superior)
			if err != nil {
				return err
			}
			params["superior"] = ref
		}
		raw, _ := json.Marshal(params)
		result, err := call(socket, "crm.save", raw, timeout, 100)
		if err != nil {
			return err
		}
		fmt.Fprintf(stderr, "malachi-devmail: saved %q to the CRM: %s\n", m.Subject, result)
	}
	return nil
}

// folderNamed finds the account's folder by its server name.
func folderNamed(socket, account, name string, timeout time.Duration) (string, error) {
	raw, _ := json.Marshal(map[string]any{"accountId": account})
	result, err := call(socket, "folder.list", raw, timeout, 101)
	if err != nil {
		return "", err
	}
	var list struct {
		Folders []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(result, &list); err != nil {
		return "", err
	}
	for _, f := range list.Folders {
		if strings.EqualFold(f.Name, name) {
			return f.ID, nil
		}
	}
	return "", fmt.Errorf("eway: no folder %q in the mail account (not synchronised yet?)", name)
}

// messageBySubject waits for and returns the newest message of the
// folder with the subject.
func messageBySubject(socket, account, folder, subject string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		raw, _ := json.Marshal(map[string]any{"accountId": account, "folderId": folder, "page": map[string]any{"limit": 200}})
		result, err := call(socket, "message.list", raw, timeout, 102)
		if err != nil {
			return "", err
		}
		var list struct {
			Messages []struct {
				ID      string    `json:"id"`
				Subject string    `json:"subject"`
				Date    time.Time `json:"date"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(result, &list); err != nil {
			return "", err
		}
		best := ""
		var bestAt time.Time
		for _, m := range list.Messages {
			if m.Subject == subject && (best == "" || m.Date.After(bestAt)) {
				best, bestAt = m.ID, m.Date
			}
		}
		if best != "" {
			return best, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("eway: no message %q in the folder after %s", subject, timeout)
		}
		time.Sleep(time.Second)
	}
}

// call sends one request to the daemon and returns its result.
func call(socket, method string, params json.RawMessage, timeout time.Duration, id int) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to %s: %w", socket, err)
	}
	defer conn.Close()
	r := bufio.NewReaderSize(conn, 64<<10)
	if err := api.ClientHandshake(ctx, conn, r, api.KeyPath(socket)); err != nil {
		return nil, fmt.Errorf("handshake: %w", err)
	}
	req, _ := json.Marshal(api.Request{JSONRPC: api.JSONRPCVersion, ID: json.RawMessage(fmt.Sprint(id)), Method: method, Params: params})
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	for {
		line, err := readLine(r)
		if err != nil {
			return nil, fmt.Errorf("waiting for the answer: %w", err)
		}
		var env api.Envelope
		if json.Unmarshal(line, &env) != nil || env.IsNotification() {
			continue
		}
		if env.Error != nil {
			b, _ := json.MarshalIndent(env.Error, "", "  ")
			return nil, fmt.Errorf("%s: %s", method, b)
		}
		return env.Result, nil
	}
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		line = append(line, chunk...)
		if len(line) > maxLine {
			return nil, errors.New("line too long")
		}
		if !isPrefix {
			return line, nil
		}
	}
}
