package hub

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

// MessageType is the envelope's own `type` field (adr-004-ws-contract-draft.md).
type MessageType string

// The envelope's `type` values, client->server and server->client
// (adr-004-ws-contract-draft.md): subscribe/unsubscribe are client-
// sent; snapshot/candle/candle_closed/candle_corrected/status/
// heartbeat/error are server-sent.
const (
	TypeSubscribe       MessageType = "subscribe"
	TypeUnsubscribe     MessageType = "unsubscribe"
	TypeSnapshot        MessageType = "snapshot"
	TypeCandle          MessageType = "candle"
	TypeCandleClosed    MessageType = "candle_closed"
	TypeCandleCorrected MessageType = "candle_corrected"
	TypeStatus          MessageType = "status"
	TypeHeartbeat       MessageType = "heartbeat"
	TypeError           MessageType = "error"
)

// envelopeVersion is the WS contract's own schema version
// (adr-004-ws-contract-draft.md's own "v" field). 1 for this draft.
const envelopeVersion = 1

// Envelope is every message, both directions, over /ws
// (adr-004-ws-contract-draft.md). id/seq are server-assigned and
// meaningless from the client.
type Envelope struct {
	V       int             `json:"v"`
	Type    MessageType     `json:"type"`
	ID      string          `json:"id,omitempty"`
	TS      time.Time       `json:"ts"`
	Sub     string          `json:"sub,omitempty"`
	Seq     uint64          `json:"seq,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewEventID returns a server-assigned event identifier: 16 bytes from
// crypto/rand, hex-encoded -- the standard library alone (brief non-
// negotiable: "no new dependency," and the review's own smaller
// correction names this choice explicitly), collision-resistant to the
// same degree a random UUIDv4 is, without needing that exact RFC 4122
// byte layout, which this id has no reason to follow.
func NewEventID() string {
	var b [16]byte
	// crypto/rand.Read on the standard library's own io.Reader never
	// returns a short read without an error (guaranteed by the
	// stdlib's own doc comment); a non-nil error here means the OS
	// entropy source itself is broken, which nothing in this process
	// can recover from -- panic is the only option other than silently
	// returning a predictable (and so, forgeable) id.
	if _, err := rand.Read(b[:]); err != nil {
		panic("hub: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
