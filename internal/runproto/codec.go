// Copyright (C) 2026 Techdelight BV

package runproto

import (
	"encoding/json"
	"io"
	"sync"
)

// Encoder writes Messages as newline-terminated JSON to a writer.
// Multiple goroutines may call Encode concurrently — writes are
// serialised so frames never interleave on the wire.
type Encoder struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// NewEncoder returns an Encoder writing to w. json.Encoder.Encode emits
// a trailing newline after each value, which is the frame delimiter.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{enc: json.NewEncoder(w)}
}

// Encode writes a single message frame.
func (e *Encoder) Encode(m Message) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.enc.Encode(m)
}

// Decoder reads newline-delimited JSON message frames from a reader.
// Decoder is not safe for concurrent use; one reader per connection.
type Decoder struct {
	dec *json.Decoder
}

// NewDecoder returns a Decoder reading from r. json.Decoder accepts
// values separated by any whitespace, so it transparently handles the
// newline framing emitted by Encoder.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{dec: json.NewDecoder(r)}
}

// Decode reads the next message. Returns io.EOF when the stream ends
// cleanly between frames.
func (d *Decoder) Decode() (Message, error) {
	var m Message
	if err := d.dec.Decode(&m); err != nil {
		return Message{}, err
	}
	return m, nil
}
