// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package chatgpt

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	Issuer        = "https://auth.openai.com"
	Resource      = "https://api.openai.com/v1"
	Scope         = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
	DynamicClient = "dynamic_agent_client"
	CallbackPath  = "/auth/callback"
	UsageURL      = "https://chatgpt.com/settings/usage"
)

func randomValue() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func hostID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func challenge(s string) string {
	b := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func authorizationURL(r Registration, redirect, state, nonce, verifier string) (string, error) {
	u, err := url.Parse(redirect)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != CallbackPath || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", authError("invalid_response")
	}
	client := r.ClientID
	if client == "" {
		client = DynamicClient
	}
	v := url.Values{"client_id": {client}, "ext_agent_host_id": {r.HostID}, "response_type": {"code"}, "redirect_uri": {redirect}, "scope": {Scope}, "resource": {Resource}, "state": {state}, "nonce": {nonce}, "code_challenge_method": {"S256"}, "code_challenge": {challenge(verifier)}}
	if r.ClientID == "" {
		v.Set("agent_name_hint", "Malachi Mail")
	} else if r.Email != "" {
		v.Set("login_hint", r.Email)
	}
	return Issuer + "/api/accounts/authorize?" + v.Encode(), nil
}
func parseCallback(callback, redirect, state string) (url.Values, error) {
	u, err := url.Parse(callback)
	if err != nil || u.Fragment != "" || u.User != nil {
		return nil, authError("invalid_response")
	}
	base := *u
	base.RawQuery = ""
	if base.String() != redirect {
		return nil, authError("invalid_response")
	}
	v, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, authError("invalid_response")
	}
	for _, a := range v {
		if len(a) != 1 {
			return nil, authError("invalid_response")
		}
	}
	if subtle.ConstantTimeCompare([]byte(v.Get("state")), []byte(state)) != 1 {
		return nil, authError("invalid_response")
	}
	return v, nil
}
func hasScope(s string) bool {
	set := map[string]bool{}
	for _, x := range strings.Fields(s) {
		set[x] = true
	}
	for _, x := range strings.Fields(Scope) {
		if !set[x] {
			return false
		}
	}
	return true
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
}
type protocol struct {
	http *http.Client
	now  func() time.Time
}

func (p protocol) json(ctx context.Context, method, endpoint string, fields url.Values, out any) (int, error) {
	var body io.Reader
	if fields != nil {
		body = strings.NewReader(fields.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, authError("invalid_response")
	}
	if fields != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := p.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, authError("network")
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, (256<<10)+1))
	if err != nil {
		return res.StatusCode, authError("network")
	}
	if len(b) > 256<<10 || json.Unmarshal(b, out) != nil {
		return res.StatusCode, authError("invalid_response")
	}
	return res.StatusCode, nil
}
func (p protocol) exchange(ctx context.Context, v url.Values) (tokenResponse, error) {
	var r tokenResponse
	status, err := p.json(ctx, "POST", Issuer+"/api/accounts/oauth/token", v, &r)
	if err != nil {
		return r, err
	}
	if status < 200 || status >= 300 {
		code := "permission_denied"
		if r.Error == "invalid_grant" {
			code = "reconnect_required"
		} else if status >= 500 {
			code = "network"
		}
		return r, authError(code)
	}
	if strings.TrimSpace(r.AccessToken) == "" || !strings.EqualFold(r.TokenType, "Bearer") || r.ExpiresIn <= 0 || r.ExpiresIn > 7*24*60*60 {
		return r, authError("invalid_response")
	}
	return r, nil
}
func trustedEndpoint(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host == "auth.openai.com" && u.User == nil && u.Fragment == ""
}
func (p protocol) discovery(ctx context.Context) (map[string]json.RawMessage, error) {
	var r map[string]json.RawMessage
	status, err := p.json(ctx, "GET", Issuer+"/.well-known/openid-configuration", nil, &r)
	if err != nil {
		return nil, err
	}
	if status != 200 || str(r, "issuer") != Issuer {
		return nil, authError("invalid_response")
	}
	return r, nil
}
func str(m map[string]json.RawMessage, k string) string {
	var s string
	_ = json.Unmarshal(m[k], &s)
	return s
}
func number(m map[string]json.RawMessage, k string) (int64, bool) {
	if len(m[k]) == 0 || string(m[k]) == "null" {
		return 0, false
	}
	var n int64
	err := json.Unmarshal(m[k], &n)
	return n, err == nil
}

// validate uses Go's maintained RSA verifier. Unverified claims are never identity.
func (p protocol) validate(ctx context.Context, token, client, nonce, subject string) (string, string, error) {
	invalid := func() (string, string, error) { return "", "", authError("invalid_response") }
	if len(token) > 64<<10 {
		return invalid()
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return invalid()
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return invalid()
	}
	var header map[string]json.RawMessage
	if !uniqueJSON(headerBytes) || json.Unmarshal(headerBytes, &header) != nil || str(header, "alg") != "RS256" || str(header, "kid") == "" {
		return invalid()
	}
	discovery, err := p.discovery(ctx)
	if err != nil {
		return "", "", err
	}
	endpoint := str(discovery, "jwks_uri")
	if !trustedEndpoint(endpoint) {
		return invalid()
	}
	var keys struct {
		Keys []struct{ KTY, Kid, Alg, Use, N, E string }
	}
	status, err := p.json(ctx, "GET", endpoint, nil, &keys)
	if err != nil {
		return "", "", err
	}
	if status != 200 {
		return invalid()
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return invalid()
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	verified := false
	for _, k := range keys.Keys {
		if k.KTY != "RSA" || k.Kid != str(header, "kid") || (k.Alg != "" && k.Alg != "RS256") || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, ne := base64.RawURLEncoding.DecodeString(k.N)
		e, ee := base64.RawURLEncoding.DecodeString(k.E)
		if ne != nil || ee != nil || len(n) < 256 || len(e) > 4 || len(e) == 0 {
			continue
		}
		exp := 0
		for _, b := range e {
			exp = exp<<8 | int(b)
		}
		if exp < 3 || exp%2 == 0 {
			continue
		}
		key := rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}
		if rsa.VerifyPKCS1v15(&key, crypto.SHA256, digest[:], signature) == nil {
			verified = true
			break
		}
	}
	if !verified {
		return invalid()
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return invalid()
	}
	var c map[string]json.RawMessage
	if !uniqueJSON(payload) || json.Unmarshal(payload, &c) != nil || str(c, "iss") != Issuer {
		return invalid()
	}
	var auds []string
	var aud string
	if json.Unmarshal(c["aud"], &aud) == nil {
		auds = []string{aud}
	} else if json.Unmarshal(c["aud"], &auds) != nil {
		return invalid()
	}
	found := false
	for _, a := range auds {
		found = found || a == client
	}
	if !found {
		return invalid()
	}
	azp := str(c, "azp")
	if (len(auds) > 1 || azp != "") && azp != client {
		return invalid()
	}
	now := p.now().Unix()
	exp, exOK := number(c, "exp")
	iat, iaOK := number(c, "iat")
	if !exOK || !iaOK || exp <= now-60 || iat > now+60 || iat >= exp {
		return invalid()
	}
	if raw, ok := c["nbf"]; ok {
		var nbf int64
		if json.Unmarshal(raw, &nbf) != nil || nbf > now+60 || nbf >= exp {
			return invalid()
		}
	}
	sub := str(c, "sub")
	if strings.TrimSpace(sub) == "" || (nonce != "" && subtle.ConstantTimeCompare([]byte(str(c, "nonce")), []byte(nonce)) != 1) {
		return invalid()
	}
	if subject != "" && subject != sub {
		return "", "", authError("identity_mismatch")
	}
	return sub, str(c, "email"), nil
}
func (p protocol) revoke(ctx context.Context, client, token string) (bool, error) {
	r, err := p.discovery(ctx)
	if err != nil {
		return false, err
	}
	endpoint := str(r, "revocation_endpoint")
	if !trustedEndpoint(endpoint) {
		return false, authError("invalid_response")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(url.Values{"token": {token}, "token_type_hint": {"refresh_token"}, "client_id": {client}}.Encode()))
	if err != nil {
		return false, authError("invalid_response")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := p.http.Do(req)
	if err != nil {
		return false, authError("network")
	}
	res.Body.Close()
	return res.StatusCode == 200, nil
}

func (r *tokenResponse) UnmarshalJSON(b []byte) error {
	type plain tokenResponse
	var value plain
	if err := json.Unmarshal(b, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	for _, k := range []string{"refresh_token", "id_token"} {
		if raw, ok := fields[k]; ok {
			var token string
			if json.Unmarshal(raw, &token) != nil || strings.TrimSpace(token) == "" {
				return errors.New("invalid optional token")
			}
		}
	}
	if _, present := fields["scope"]; present && strings.TrimSpace(value.Scope) == "" {
		return errors.New("invalid scope")
	}
	*r = tokenResponse(value)
	return nil
}

// JSON identity objects require unique member names to prevent claim ambiguity.
func uniqueJSON(b []byte) bool {
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.UseNumber()
	var read func(int) bool
	read = func(depth int) bool {
		if depth > 64 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				name, err := decoder.Token()
				key, ok := name.(string)
				if err != nil || !ok || seen[key] {
					return false
				}
				seen[key] = true
				if !read(depth + 1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for decoder.More() {
				if !read(depth + 1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if !read(0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}
