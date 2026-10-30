package store

// Call entries follow src/archive/calls.rs: the calls of a chat are
// messages of kind "call" (Message.Call), from the phone's call-log messages
// in history and from the records this app files itself, under ids that
// start with CallEntryPrefix.

const (
	// CallEntryPrefix starts the id of a call entry this app wrote.
	CallEntryPrefix = "zapfast-call-"
	// CallEntryWindow is how far apart, in seconds, two entries of one call
	// may be: the phone's and this app's.
	CallEntryWindow = 120
	// CallLogLimit is how many entries the Calls page lists.
	CallLogLimit = 500
)

// CallLog lists the account's call entries, newest first, at most limit.
func (s *Archive) CallLog(limit int) ([]Message, error) {
	if limit <= 0 {
		limit = CallLogLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT m.payload,m.raw FROM messages m
JOIN message_flags f ON f.chat=m.chat AND f.id=m.id
WHERE f.kind=? ORDER BY m.ts DESC,m.ord DESC,m.id DESC LIMIT ?`, KindCall, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// CallEntryNear reports whether the chat already has an entry, other than
// except, for a call placed by the same side within CallEntryWindow of
// timestamp: the phone's own entry for a call this app recorded, or the
// other way round.
func (s *Archive) CallEntryNear(chat string, timestamp int64, fromMe bool, except string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages m JOIN message_flags f ON f.chat=m.chat AND f.id=m.id
WHERE m.chat=? AND f.kind=? AND f.from_me=? AND m.id<>? AND m.ts BETWEEN ? AND ?)`,
		chat, KindCall, fromMe, except, timestamp-CallEntryWindow, timestamp+CallEntryWindow).Scan(&found)
	return found, err
}

// LocalCallTwins are this app's own entries (CallEntryPrefix) of the same
// side within CallEntryWindow of timestamp: the ones to drop once the phone
// files the call itself.
func (s *Archive) LocalCallTwins(chat string, timestamp int64, fromMe bool) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return queryStrings(s.db, `SELECT m.id FROM messages m JOIN message_flags f ON f.chat=m.chat AND f.id=m.id
WHERE m.chat=? AND f.kind=? AND f.from_me=? AND substr(m.id,1,?)=? AND m.ts BETWEEN ? AND ?
ORDER BY m.ts,m.ord,m.id`,
		chat, KindCall, fromMe, len(CallEntryPrefix), CallEntryPrefix, timestamp-CallEntryWindow, timestamp+CallEntryWindow)
}
