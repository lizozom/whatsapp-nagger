package messenger

import "strings"

// Allowlist is the set of phone numbers (international format, no `+`) the bot
// will operate on behalf of. A WhatsApp group is gated in iff at least one of
// its participants' phone numbers is in this set (NFR2 — checked every message,
// not cached per-group).
//
// Parsed once from ALLOWED_PHONES at startup. Empty/unset env produces an
// empty allowlist that drops every group (deliberate fail-closed default).
type Allowlist struct {
	phones map[string]struct{}
}

// ParseAllowlist parses a comma-separated list of phones.
// Whitespace and empty entries are tolerated.
func ParseAllowlist(env string) *Allowlist {
	a := &Allowlist{phones: make(map[string]struct{})}
	for _, p := range strings.Split(env, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		a.phones[p] = struct{}{}
	}
	return a
}

// Allows reports whether the given phone is in the allowlist.
func (a *Allowlist) Allows(phone string) bool {
	if a == nil {
		return false
	}
	_, ok := a.phones[phone]
	return ok
}

// FilterAllowed returns the input phones that are in the allowlist,
// preserving input order. Used by AutoCreate to decide who to seed as members.
func (a *Allowlist) FilterAllowed(phones []string) []string {
	if a == nil {
		return nil
	}
	out := make([]string, 0, len(phones))
	for _, p := range phones {
		if _, ok := a.phones[p]; ok {
			out = append(out, p)
		}
	}
	return out
}

// Size returns the number of phones in the allowlist (for startup logging).
func (a *Allowlist) Size() int {
	if a == nil {
		return 0
	}
	return len(a.phones)
}
