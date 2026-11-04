// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package chatgpt

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type rpcReply struct {
	result json.RawMessage
	err    error
}
type rpcClient struct {
	cmd      *exec.Cmd
	input    io.WriteCloser
	writeMu  sync.Mutex
	mu       sync.Mutex
	next     int
	pending  map[string]chan rpcReply
	notify   func(string, json.RawMessage)
	request  func(string, json.RawMessage) any
	done     chan struct{}
	stopOnce sync.Once
	status   int
}

func startRPC(path string, args, env []string, directory string) (*rpcClient, error) {
	cmd := exec.Command(path, args...)
	cmd.Env = env
	cmd.Dir = directory
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.New("codex_process_failed")
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.New("codex_process_failed")
	}
	cmd.Stderr = io.Discard
	c := &rpcClient{cmd: cmd, input: input, pending: map[string]chan rpcReply{}, done: make(chan struct{}), status: -1}
	if err = cmd.Start(); err != nil {
		return nil, errors.New("codex_process_failed")
	}
	go c.read(out)
	return c, nil
}
func (c *rpcClient) read(out io.ReadCloser) {
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 8192), frameLimit)
	for scan.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if !uniqueJSON(scan.Bytes()) || json.Unmarshal(scan.Bytes(), &m) != nil {
			c.Terminate()
			break
		}
		if m.Method != "" {
			c.mu.Lock()
			notification, request := c.notify, c.request
			c.mu.Unlock()
			if len(m.ID) > 0 && string(m.ID) != "null" {
				go func(id json.RawMessage, method string, params json.RawMessage) {
					var result any = map[string]any{"error": map[string]any{"code": -32601, "message": "request denied by Malachi Mail policy"}}
					if request != nil {
						result = request(method, params)
					}
					_ = c.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
				}(m.ID, m.Method, m.Params)
			} else if notification != nil {
				notification(m.Method, m.Params)
			}
			continue
		}
		if len(m.ID) == 0 {
			c.Terminate()
			break
		}
		c.mu.Lock()
		pending := c.pending[string(m.ID)]
		delete(c.pending, string(m.ID))
		c.mu.Unlock()
		if pending != nil {
			reply := rpcReply{result: m.Result}
			if len(m.Error) > 0 && string(m.Error) != "null" {
				reply.err = errors.New("codex_rpc_failed")
			}
			pending <- reply
		}
	}
	// A malformed/broken stream may leave a child alive; stop its process group.
	if scan.Err() != nil {
		c.Terminate()
	}
	_ = out.Close()
	_ = c.cmd.Wait()
	if c.cmd.ProcessState != nil {
		c.status = c.cmd.ProcessState.ExitCode()
	}
	c.mu.Lock()
	for key, ch := range c.pending {
		ch <- rpcReply{err: errors.New("codex_session_closed")}
		delete(c.pending, key)
	}
	c.mu.Unlock()
	close(c.done)
}
func (c *rpcClient) setHandlers(notification func(string, json.RawMessage), request func(string, json.RawMessage) any) {
	c.mu.Lock()
	c.notify, c.request = notification, request
	c.mu.Unlock()
}
func (c *rpcClient) write(value any) error {
	b, err := json.Marshal(value)
	if err != nil || len(b) > frameLimit {
		return errors.New("codex_frame_limit")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.input.Write(append(b, '\n'))
	if err != nil {
		return errors.New("codex_session_closed")
	}
	return nil
}
func (c *rpcClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.next++
	id := c.next
	key := strconv.Itoa(id)
	reply := make(chan rpcReply, 1)
	c.pending[key] = reply
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, key); c.mu.Unlock() }()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case r := <-reply:
		return r.result, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, errors.New("codex_session_closed")
	}
}
func (c *rpcClient) Notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
func (c *rpcClient) Terminate() {
	c.stopOnce.Do(func() {
		_ = c.input.Close()
		if c.cmd.Process == nil {
			return
		}
		pid := c.cmd.Process.Pid
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		go func() {
			select {
			case <-c.done:
			case <-time.After(time.Second):
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}()
	})
}
func (c *rpcClient) Running() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}
