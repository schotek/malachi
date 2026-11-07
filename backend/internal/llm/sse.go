// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package llm

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
)

// readFailed is a failed read of the stream (the connection dropped, a
// timeout), as opposed to fn's own error, which readSSE returns as it is.
type readFailed struct{ err error }

func (e *readFailed) Error() string { return e.err.Error() }
func (e *readFailed) Unwrap() error { return e.err }

// readSSE reads a server-sent event stream from r and calls fn for each
// event with its event name ("" when none) and its data (the data lines
// joined with "\n"). Lines end with "\n" or "\r\n" and are at most
// MaxLineBytes; an event's data is at most MaxEventBytes; the whole
// stream at most MaxStreamBytes (beyond any of these the stream is a
// protocol error). Comment lines (":"), "id:" and "retry:" are ignored,
// as are unknown fields. A blank line ends an event; an event without
// data is not delivered; an unterminated last event is delivered when it
// has data. fn's error ends the reading and is returned as it is; a read
// error is returned as a *readFailed.
func readSSE(r io.Reader, fn func(event, data string) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var (
		event   string
		data    strings.Builder
		hasData bool
		total   int64
	)
	flush := func() error {
		if !hasData {
			event = ""
			return nil
		}
		ev, d := event, data.String()
		event = ""
		data.Reset()
		hasData = false
		return fn(ev, d)
	}
	for {
		line, err := readLine(br)
		total += int64(len(line)) + 1
		if total > MaxStreamBytes {
			return protocolError("the stream is longer than the package reads")
		}
		if len(line) > 0 {
			if perr := sseLine(line, &event, &data, &hasData); perr != nil {
				return perr
			}
		} else if err == nil {
			if ferr := flush(); ferr != nil {
				return ferr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return flush()
			}
			var e *Error
			if errors.As(err, &e) {
				return err
			}
			return &readFailed{err}
		}
	}
}

// readLine reads one line without its terminator; a line longer than
// MaxLineBytes is a protocol error. A final line without a terminator
// is returned with io.EOF.
func readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := br.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > MaxLineBytes+1 {
			return nil, protocolError("a line of the stream is longer than the package reads")
		}
		if err == nil {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		// io.EOF or a read error: whatever was read is the last line.
		return bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r")), err
	}
	line = bytes.TrimSuffix(line, []byte("\n"))
	line = bytes.TrimSuffix(line, []byte("\r"))
	return line, nil
}

// sseLine applies one non-empty line to the event being assembled.
func sseLine(line []byte, event *string, data *strings.Builder, hasData *bool) error {
	if line[0] == ':' {
		return nil
	}
	field, value, _ := bytes.Cut(line, []byte(":"))
	value = bytes.TrimPrefix(value, []byte(" "))
	switch string(field) {
	case "event":
		*event = string(value)
	case "data":
		if data.Len()+len(value)+1 > MaxEventBytes {
			return protocolError("an event of the stream is longer than the package reads")
		}
		if *hasData {
			data.WriteByte('\n')
		}
		data.Write(value)
		*hasData = true
	}
	return nil
}
