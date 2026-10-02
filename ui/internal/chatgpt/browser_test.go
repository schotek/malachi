// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package chatgpt

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBrowserBoundCallbackAndState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	redirect := ""
	requestsDone := make(chan struct{})
	b := NewBrowser(func(auth string) error {
		u, _ := url.Parse(auth)
		redirect = u.Query().Get("redirect_uri")
		endpoint, _ := url.Parse(redirect)
		if listener, err := net.Listen("tcp4", endpoint.Host); err == nil {
			listener.Close()
			t.Fatal("browser launched before callback socket reservation")
		}
		go func() {
			defer close(requestsDone)
			for _, callback := range []string{redirect + "?state=wrong", redirect + "?state=state&state=state"} {
				res, err := http.Get(callback)
				if err != nil {
					t.Error(err)
					return
				}
				res.Body.Close()
				if res.StatusCode != 400 {
					t.Errorf("invalid callback accepted %d", res.StatusCode)
				}
			}
			res, err := http.Get(redirect + "?state=state&code=code&client_id=issued")
			if err != nil {
				t.Error(err)
				return
			}
			res.Body.Close()
		}()
		return nil
	})
	callback, err := b.Authorize(ctx, func(redirect string) (string, error) {
		return authorizationURL(Registration{HostID: hostID()}, redirect, "state", "nonce", "verifier")
	}, "state")
	if err != nil || !strings.HasPrefix(callback, redirect+"?") {
		t.Fatalf("callback %q %v", callback, err)
	}
	select {
	case <-requestsDone:
	case <-ctx.Done():
		t.Fatal("browser client did not finish")
	}
}
func TestBrowserCancellationAndLaunchFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		b := NewBrowser(func(string) error {
			if fail {
				return authError("browser")
			}
			cancel()
			return nil
		})
		_, err := b.Authorize(ctx, func(redirect string) (string, error) {
			return authorizationURL(Registration{HostID: hostID()}, redirect, "s", "n", "v")
		}, "s")
		cancel()
		if err == nil {
			t.Fatal("cancellation/launch failure accepted")
		}
	}
}
