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

// effectiveClass is the class this caller is actually TREATED as, and it is the
// single source every other method derives from.
//
// Only an explicitly-human class counts as human. The zero value, and anything
// unrecognised, is an agent — the same rule as parseCallerClass and TierFor,
// because human is the privileged answer and must be proven rather than assumed.
// Deriving IsAgent/Actor/String from one place is deliberate: an earlier version
// answered these three questions independently and gave three different answers
// for `Caller{}` (not-an-agent, actor "system", string "human"), which is the
// shape of inconsistency that hides a privilege bug.
func (c Caller) effectiveClass() CallerClass {
	if c.Class == CallerHuman {
		return CallerHuman
	}
	return CallerAgent
}

// IsAgent reports whether this caller is treated as an agent client — true for
// anything that is not explicitly human.
func (c Caller) IsAgent() bool { return c.effectiveClass() != CallerHuman }

// Actor is the event-log label for this caller. It is derived, never supplied.
func (c Caller) Actor() string {
	if c.effectiveClass() == CallerHuman {
		return ActorHuman
	}
	return ActorAgent
}

// String renders the class this caller is treated as, so a refusal message never
// claims a privilege the caller does not have.
func (c Caller) String() string { return string(c.effectiveClass()) }

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
