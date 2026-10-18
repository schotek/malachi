// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eway

import (
	"context"
	"crypto/md5" //nolint:gosec // eWay-CRM's LogIn takes the MD5 hex of the password; not used for security here
	"encoding/hex"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// User is the signed-in user.
type User struct {
	GUID  string
	Email string // "" when the user record has none (T0 2)
}

// logIn signs in (docs/eway.md D4): the MD5 hex of the password, never
// logged. rcLoginFailed (wrong password or unknown user, T0 2) latches the
// client: it signs in no more until it is rebuilt with other credentials,
// so a wrong password cannot lock the user out. A code naming the licence
// is crmSignInUnsupported with reason "plan" (not verifiable on the trial).
func (c *Client) logIn(ctx context.Context) (string, error) {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return "", errStopped()
	}
	if c.failed != nil {
		err := c.failed
		c.mu.Unlock()
		return "", err
	}
	if c.session != "" {
		sid := c.session
		c.mu.Unlock()
		return sid, nil
	}
	c.mu.Unlock()
	if c.password == nil {
		return "", api.NewError(api.CodeAuthRequired, "eway: no password")
	}
	pw, err := c.password(ctx)
	if err != nil {
		return "", err
	}
	if pw == "" {
		return "", api.NewError(api.CodeAuthRequired, "eway: no password")
	}
	sum := md5.Sum([]byte(pw)) //nolint:gosec // the service's protocol
	body := map[string]any{
		"userName":                c.username,
		"passwordHash":            hex.EncodeToString(sum[:]),
		"appVersion":              AppVersion,
		"clientMachineIdentifier": c.clientID,
		"clientMachineName":       MachineName,
	}
	env, err := c.post(ctx, "LogIn", body, "", loginTimeout)
	if err != nil {
		c.record(ctx, asAPI(err))
		return "", err
	}
	switch rc := string(env.ReturnCode); {
	case rc == rcSuccess:
	case rc == rcLoginFailed:
		e := api.NewError(api.CodeAuthFailed, "eway: sign-in refused (%s)", rcLoginFailed)
		c.mu.Lock()
		c.failed = e
		c.mu.Unlock()
		c.record(ctx, e)
		return "", e
	case strings.Contains(strings.ToLower(rc), "licen"):
		e := &api.Error{Code: api.CodeCrmSignInUnsupported, Message: "eway: sign-in refused (" + rcClass(rc) + ")",
			Data: api.CrmSignInUnsupportedData{Reason: api.CrmSignInPlan}}
		c.record(ctx, e)
		return "", e
	default:
		e := api.NewError(api.CodeServerError, "eway: LogIn: %s", rcClass(rc))
		c.record(ctx, e)
		return "", e
	}
	sid := strings.TrimSpace(string(env.SessionID))
	if sid == "" || len(sid) > 256 {
		e := api.NewError(api.CodeServerError, "eway: LogIn answered without a session")
		c.record(ctx, e)
		return "", e
	}
	c.mu.Lock()
	if c.stopped {
		// Stopped while signing in: the new session is not kept.
		c.mu.Unlock()
		c.logOutSession(context.WithoutCancel(ctx), sid)
		return "", errStopped()
	}
	c.session = sid
	if g := cleanGUID(string(env.UserItemGUID)); g != "" {
		c.user = g
	}
	c.mu.Unlock()
	c.record(ctx, nil)
	c.log.Debug("eway signed in")
	return sid, nil
}

// dropSession forgets sid (after rcBadSession) unless another call already
// replaced it.
func (c *Client) dropSession(sid string) {
	c.mu.Lock()
	if c.session == sid {
		c.session = ""
	}
	c.mu.Unlock()
}

// call runs a method with the session in its body: signs in when there is
// no session, and once more when the service answers rcBadSession (idle
// timeout, the day's limit, a new IP address); a second rcBadSession is
// serverError. Other return codes are the caller's.
func (c *Client) call(ctx context.Context, method string, params map[string]any) (*envelope, error) {
	for attempt := 0; ; attempt++ {
		sid, err := c.logIn(ctx)
		if err != nil {
			return nil, err
		}
		body := make(map[string]any, len(params)+1)
		for k, v := range params {
			body[k] = v
		}
		body["sessionId"] = sid
		env, err := c.post(ctx, method, body, sid, 0)
		if err != nil {
			c.record(ctx, asAPI(err))
			return nil, err
		}
		if string(env.ReturnCode) == rcBadSession {
			c.dropSession(sid)
			if attempt == 0 {
				c.log.Debug("eway session expired, signing in again")
				continue
			}
			return nil, api.NewError(api.CodeServerError, "eway: %s: session refused twice", method)
		}
		c.record(ctx, nil)
		return env, nil
	}
}

// ok is call with any return code but rcSuccess an error (serverError
// naming the method and the code).
func (c *Client) ok(ctx context.Context, method string, params map[string]any) (*envelope, error) {
	env, err := c.call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	if rc := string(env.ReturnCode); rc != rcSuccess {
		return env, &rcError{method: method, rc: rcClass(rc)}
	}
	return env, nil
}

// rcError is an answer with a return code other than rcSuccess.
type rcError struct{ method, rc string }

func (e *rcError) Error() string { return "eway: " + e.method + ": " + e.rc }

// asAPI maps any error of this package to *api.Error.
func asAPI(err error) *api.Error {
	if err == nil {
		return nil
	}
	if ae, ok := err.(*api.Error); ok {
		return ae
	}
	if re, ok := err.(*rcError); ok {
		return api.NewError(api.CodeServerError, "eway: %s: %s", re.method, re.rc)
	}
	return api.NewError(api.CodeServerError, "eway: %s", err.Error())
}

// ToAPIError maps an error of this package to the contract; nil for nil.
func ToAPIError(err error) *api.Error { return asAPI(err) }

// SignIn signs in now (a fresh session even if one exists) and reads the
// user's e-mail (account.test, account.add). It clears nothing of the
// latch: a client that failed stays failed.
func (c *Client) SignIn(ctx context.Context) (User, error) {
	c.mu.Lock()
	c.session = ""
	c.mu.Unlock()
	if _, err := c.logIn(ctx); err != nil {
		return User{}, err
	}
	c.mu.Lock()
	u := User{GUID: c.user}
	c.mu.Unlock()
	if u.GUID == "" {
		return u, nil
	}
	// Only the e-mail of the user record is read; the record also carries
	// fields such as Server_Password (T0 2) and is never logged or kept.
	env, err := c.ok(ctx, "GetUsersByItemGuids", map[string]any{"itemGuids": []string{u.GUID}})
	if err != nil {
		return u, nil // the e-mail is optional
	}
	items, err := decodeItems(env.Data)
	if err == nil && len(items) > 0 {
		u.Email = CleanEmail(string(items[0].Email1Address))
	}
	return u, nil
}

// LogOut ends the session, best effort within 2 s; the client may sign in
// again later (unless it is closed).
func (c *Client) LogOut(ctx context.Context) {
	c.mu.Lock()
	sid := c.session
	c.session = ""
	c.mu.Unlock()
	c.logOutSession(ctx, sid)
}

// Close stops the client for good (the account was paused, removed or
// edited, or the daemon stops): its context is cancelled, no call signs in
// again (unavailable), and the session is logged out.
func (c *Client) Close(ctx context.Context) {
	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()
	c.cancel()
	c.LogOut(ctx)
}

// Context is cancelled when the client is closed; background work of the
// account (a stale record's check) derives its context from it.
func (c *Client) Context() context.Context { return c.ctx }

func errStopped() *api.Error {
	return api.NewError(api.CodeUnavailable, "eway: the account is stopped")
}

func (c *Client) logOutSession(ctx context.Context, sid string) {
	if sid == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := c.post(ctx, "LogOut", map[string]any{"sessionId": sid}, sid, 2*time.Second); err != nil {
		c.log.Debug("eway LogOut failed", "code", asAPI(err).Code.String())
	}
}

// AuthFailed is the latched sign-in failure, nil when there is none.
func (c *Client) AuthFailed() *api.Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failed
}

// record remembers the outcome of the last exchange for State: nil = it
// worked. Cancellation says nothing about the service, and neither does a
// failure after the caller's context ended (a lookup's budget ran out:
// the result is partial, the account is not offline).
func (c *Client) record(ctx context.Context, err *api.Error) {
	if err != nil && (err.Code == api.CodeCancelled || ctx.Err() != nil) {
		return
	}
	c.mu.Lock()
	c.state = stateRecord{err: err, at: c.now(), used: true}
	c.mu.Unlock()
}

// State is the account's state for account.list: idle until something
// failed; authRequired after a refused sign-in; offline after a network
// failure; error with the class otherwise.
func (c *Client) State(accountID string) api.SyncState {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := api.SyncState{AccountID: api.AccountID(accountID), Status: api.SyncIdle, Progress: -1}
	if c.state.used && c.state.err == nil {
		at := c.state.at
		st.LastSync = &at
	}
	err := c.failed
	if err == nil {
		err = c.state.err
	}
	if err == nil {
		return st
	}
	e := *err
	st.Error = &e
	switch e.Code {
	case api.CodeAuthFailed, api.CodeAuthRequired, api.CodeCrmSignInUnsupported:
		st.Status = api.SyncAuthRequired
	case api.CodeNetworkError, api.CodeServerTimeout, api.CodeOffline:
		st.Status = api.SyncOffline
	default:
		st.Status = api.SyncError
	}
	return st
}
