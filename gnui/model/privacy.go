package model

import (
	"slices"

	"zapfast/abi"
)

// numPrivacy is the number of privacy kinds (abi.PrivacyKind).
const numPrivacy = int(abi.PrivacyKindCallAdd) + 1

// Privacy is the account's privacy settings as the phone last reported them,
// with the writes still in flight applied on top (zf:src/privacy.rs:172).
// Settings → Privacy calls BeginSet and, when it says so, sends
// SetAccountPrivacy; the core's Saved or Failed event settles it.
type Privacy struct {
	values  [numPrivacy]abi.PrivacyChoice
	has     [numPrivacy]bool
	revert  [numPrivacy]abi.PrivacyChoice // the confirmed value behind a write in flight
	pending [numPrivacy]bool
	// FetchFailed: the last fetch failed, the values shown may be stale.
	FetchFailed bool
	// Loaded: a fetch has succeeded since linking.
	Loaded bool
	gen    uint32
}

// PrivacyChoices are the values Settings offers for kind, the ones the phone
// offers (zf:src/privacy.rs:68). The account can hold others (an Except
// list, an older Nobody for groups): shown, not offered.
func PrivacyChoices(kind abi.PrivacyKind) []abi.PrivacyChoice {
	switch kind {
	case abi.PrivacyKindLastSeen, abi.PrivacyKindProfile, abi.PrivacyKindAbout:
		return []abi.PrivacyChoice{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceContacts, abi.PrivacyChoiceNobody}
	case abi.PrivacyKindGroupAdd:
		return []abi.PrivacyChoice{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceContacts}
	case abi.PrivacyKindOnline:
		return []abi.PrivacyChoice{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceSameAsLastSeen}
	case abi.PrivacyKindReadReceipts:
		return []abi.PrivacyChoice{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceNobody}
	case abi.PrivacyKindCallAdd:
		return []abi.PrivacyChoice{abi.PrivacyChoiceEveryone, abi.PrivacyChoiceKnown}
	}
	return nil
}

func validKind(kind abi.PrivacyKind) bool { return int(kind) < numPrivacy }

// Get is kind's value; ok is false when the phone has not reported it.
func (p *Privacy) Get(kind abi.PrivacyKind) (abi.PrivacyChoice, bool) {
	if !validKind(kind) {
		return 0, false
	}
	return p.values[kind], p.has[kind]
}

// Pending reports whether a write of kind is in flight.
func (p *Privacy) Pending(kind abi.PrivacyKind) bool { return validKind(kind) && p.pending[kind] }

// Editable reports whether the rows can be edited: the phone's values are
// loaded and the last fetch did not fail.
func (p *Privacy) Editable() bool { return p.Loaded && !p.FetchFailed }

// Gen moves whenever a value changes.
func (p *Privacy) Gen() uint32 { return p.gen }

// applyFetch takes a fetched snapshot. A failed fetch keeps what was shown
// and marks it stale. A write still in flight keeps its value, and the
// fetched one becomes what a failure restores.
func (p *Privacy) applyFetch(values []abi.PrivacyValue, failed bool) {
	p.gen++
	p.FetchFailed = failed
	if failed {
		return
	}
	var fresh [numPrivacy]abi.PrivacyChoice
	var has [numPrivacy]bool
	for _, v := range values {
		if validKind(v.Kind) {
			fresh[v.Kind], has[v.Kind] = v.Choice, true
		}
	}
	for k := range numPrivacy {
		if !p.pending[k] {
			continue
		}
		if has[k] {
			p.revert[k] = fresh[k]
		}
		if p.has[k] {
			fresh[k], has[k] = p.values[k], true
		}
	}
	p.values, p.has = fresh, has
	p.Loaded = true
}

// BeginSet shows choice at once and remembers what to restore. It reports
// false when nothing should be written: the kind is unknown, already holds
// the value, has a write in flight, or choice is not offered for it.
func (p *Privacy) BeginSet(kind abi.PrivacyKind, choice abi.PrivacyChoice) bool {
	cur, ok := p.Get(kind)
	if !ok || cur == choice || p.pending[kind] || !slices.Contains(PrivacyChoices(kind), choice) {
		return false
	}
	p.revert[kind], p.pending[kind] = cur, true
	p.values[kind] = choice
	p.gen++
	return true
}

// finishSet: the phone confirmed a write. False for a confirmation nothing
// was waiting for (one from before an unlink).
func (p *Privacy) finishSet(kind abi.PrivacyKind) bool {
	if !p.Pending(kind) {
		return false
	}
	p.pending[kind] = false
	p.gen++
	return true
}

// failSet: the phone refused a write; the confirmed value comes back.
func (p *Privacy) failSet(kind abi.PrivacyKind) {
	if !p.Pending(kind) {
		return
	}
	p.pending[kind] = false
	p.values[kind] = p.revert[kind]
	p.gen++
}

// ReceiptsOff reports whether the account's read receipts are off: the
// phone's privacy setting, which wins over the local switch
// (zf:src/app.rs:3749-3770).
func (s *Store) ReceiptsOff() bool { return s.receiptsOff }

func (s *Store) privacyEvent(e *abi.Event) {
	p := &s.Privacy
	switch e.Tag {
	case abi.EventTagReceiptsPrivacy:
		s.receiptsOff = e.ReceiptsPrivacy.Disabled
	case abi.EventTagAccountPrivacy:
		p.applyFetch(e.AccountPrivacy.Values, e.AccountPrivacy.Failed)
		if c, ok := p.Get(abi.PrivacyKindReadReceipts); ok {
			s.receiptsOff = c != abi.PrivacyChoiceEveryone
		}
	case abi.EventTagAccountPrivacySaved:
		if k := e.AccountPrivacySaved.Kind; p.finishSet(k) && k == abi.PrivacyKindReadReceipts {
			c, ok := p.Get(k)
			s.receiptsOff = !ok || c != abi.PrivacyChoiceEveryone
		}
	case abi.EventTagAccountPrivacyFailed:
		p.failSet(e.AccountPrivacyFailed.Kind)
	}
}
