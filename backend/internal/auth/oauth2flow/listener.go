// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2flow

import (
	"net"
	"sync"
)

// maxConns caps the connections one redirect listener serves at a time.
// The browser needs one; the cap keeps a local process (or a page in the
// browser) from tying up the session with many slow connections. Further
// connections wait in the kernel's accept queue, bounded by the server's
// read timeouts, instead of being refused.
const maxConns = 4

// limitListener accepts at most n connections at once: the stdlib twin of
// golang.org/x/net/netutil.LimitListener, without the dependency.
type limitListener struct {
	net.Listener
	sem       chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newLimitListener(l net.Listener, n int) *limitListener {
	return &limitListener{Listener: l, sem: make(chan struct{}, n), done: make(chan struct{})}
}

// Accept waits for a free slot, then for a connection; a closed listener
// ends the wait.
func (l *limitListener) Accept() (net.Conn, error) {
	select {
	case l.sem <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.sem
		return nil, err
	}
	return &limitConn{Conn: c, release: func() { <-l.sem }}, nil
}

func (l *limitListener) Close() error {
	err := l.Listener.Close()
	l.closeOnce.Do(func() { close(l.done) })
	return err
}

// limitConn frees its slot once, on the first Close.
type limitConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
