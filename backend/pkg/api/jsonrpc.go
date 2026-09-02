package api

import "encoding/json"

// JSONRPCVersion is the only accepted value of the "jsonrpc" member.
const JSONRPCVersion = "2.0"

// Framing: every JSON-RPC message is a single JSON object terminated by a
// newline ("\n"). Messages never contain raw newlines (encoding/json escapes
// them). This is newline-delimited JSON; there is no Content-Length header.

// Request is a JSON-RPC 2.0 request sent by the client. Params is always a
// JSON object (by-name parameters); positional parameters are not used.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response. Exactly one of Result or Error is set.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Notification is a JSON-RPC 2.0 notification, i.e. a request without an ID.
// The backend uses it to push events to the client (see Notify* constants).
// Clients never reply to notifications.
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Envelope is the union of all three wire shapes, used when decoding an
// incoming line whose kind is not yet known (clients receive both responses
// and notifications on the same stream).
type Envelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// IsNotification reports whether the envelope carries no ID.
func (m *Envelope) IsNotification() bool {
	return len(m.ID) == 0 || string(m.ID) == "null"
}

// IsResponse reports whether the envelope is a reply rather than a call.
func (m *Envelope) IsResponse() bool {
	return m.Method == "" && (m.Result != nil || m.Error != nil)
}
