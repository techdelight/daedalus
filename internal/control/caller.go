// Copyright (C) 2026 Techdelight BV

package control

import "fmt"

// Caller identity, derived from the TRANSPORT (docs/guild-master-plan.md §4, §6).
//
// WHY NOT A REQUEST FIELD. The obvious design — an `actor` field on the request —
// is worse than having no identity at all: a client that can name its own actor
// can name "human", so the label would be an assertion by the very party it is
// meant to constrain. The identity therefore has to come from something the
// caller cannot choose.
//
// WHY NOT PEER CREDENTIALS. The next obvious design is SO_PEERCRED on the Unix
// socket. It does not work here: the control socket is `srwxr-xr-x` and the Guild
// Master's agent runs as the same uid as the human operating the CLI, so peer
// credentials separate *users*, not *caller classes*. They would report the same
// answer for both.
//
// SO THE SOCKET SPLIT IS THE MECHANISM. The daemon listens on two sockets: the
// human `control.sock` and the restricted `control-agent.sock`. Which file a
// connection arrived through is not something the connection can lie about, and
// the agent container is given exactly one of them. The caller class is fixed by
// the listener that accepted the request, before any request body is parsed.
//
// What that buys, precisely: every event carries a caller class the caller could
// not forge, and the tiered-authority table (authority.go) can refuse an agent an
// operation while allowing a human the same one. What it does NOT buy: protection
// against something that can open the human socket directly. Anything running as
// the user can do that — the boundary is the container's mount namespace, not the
// filesystem permissions, and only the agent socket is ever mounted into a
// container.
type CallerClass string

const (
	// CallerHuman is a request that arrived on the human control socket — the
	// `daedalus task` CLI, the Web UI, the TUI.
	CallerHuman CallerClass = "human"
	// CallerAgent is a request that arrived on the restricted agent socket — the
	// Guild Master via guild-control-mcp.
	CallerAgent CallerClass = "agent"
)

// Caller is the identity of whoever is making the current request.
type Caller struct {
	Class CallerClass
}

// Human returns the human caller identity — the default for the in-process
// Service, since only the CLI drives it directly.
func Human() Caller { return Caller{Class: CallerHuman} }

// Agent returns the agent caller identity.
func Agent() Caller { return Caller{Class: CallerAgent} }

// IsAgent reports whether this caller is an agent client.
func (c Caller) IsAgent() bool { return c.Class == CallerAgent }

// Actor is the event-log label for this caller. It is derived, never supplied.
func (c Caller) Actor() string {
	switch c.Class {
	case CallerAgent:
		return ActorAgent
	case CallerHuman:
		return ActorHuman
	default:
		return ActorSystem
	}
}

// String renders the class for messages.
func (c Caller) String() string {
	if c.Class == "" {
		return string(CallerHuman)
	}
	return string(c.Class)
}

// validCallerClass reports whether c is a known class. Used where a class is
// read back from storage.
func validCallerClass(c CallerClass) bool {
	return c == CallerHuman || c == CallerAgent
}

// parseCallerClass converts a stored string back to a class, defaulting to agent
// on anything unrecognised.
//
// The default is deliberate: an unreadable caller class must never be read as
// "human", because human is the privileged answer. Unknown means untrusted.
func parseCallerClass(s string) CallerClass {
	if validCallerClass(CallerClass(s)) {
		return CallerClass(s)
	}
	return CallerAgent
}

// ErrForbidden is returned when a caller class may not perform an operation at
// all — as distinct from an operation that is merely reduced to a proposal.
var ErrForbidden = fmt.Errorf("control: operation not permitted for this caller")
