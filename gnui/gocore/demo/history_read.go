package demo

import (
	"context"
	"strings"

	"zapfast/abi"
	"zapfast/core"
)

// History and SearchHistory are core.History on the demo's messages, read
// on its goroutine: what the agents' door reads of a real account's
// archive.

var _ core.History = (*Conn)(nil)

// ask runs f on the demo's goroutine and waits for it.
func ask[T any](ctx context.Context, e *Engine, f func(s *state) (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	e.put(input{ask: func(s *state) {
		v, err := f(s)
		ch <- result{v, err}
	}})
	var zero T
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-e.h.Context().Done():
		return zero, core.ErrClosed
	}
}

// plain is m's record without its thumbnail.
func plain(m *message) abi.Message {
	x := m.Message
	x.Thumbnail, x.HasThumbnail = abi.BlobRef{}, false
	return x
}

func (s *state) readable(chat string) bool {
	c := s.chat(chat)
	return c != nil && !c.Locked
}

func (c *Conn) History(ctx context.Context, chat, before string, limit int) ([]abi.Message, bool, error) {
	type page struct {
		messages []abi.Message
		more     bool
	}
	p, err := ask(ctx, c.e, func(s *state) (page, error) {
		if !s.readable(chat) {
			return page{}, core.ErrNoHistory
		}
		all := s.messages[chat]
		end := len(all)
		if before != "" {
			for i, m := range all {
				if m.id() == before {
					end = i
					break
				}
			}
		}
		start := max(end-max(limit, 1), 0)
		out := make([]abi.Message, 0, end-start)
		for _, m := range all[start:end] {
			out = append(out, plain(m))
		}
		return page{out, start > 0}, nil
	})
	return p.messages, p.more, err
}

func (c *Conn) SearchHistory(ctx context.Context, chat, query string, limit int) ([]abi.Message, error) {
	return ask(ctx, c.e, func(s *state) ([]abi.Message, error) {
		if !s.readable(chat) {
			return nil, core.ErrNoHistory
		}
		q := strings.ToLower(query)
		var out []abi.Message
		all := s.messages[chat]
		for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
			if strings.Contains(strings.ToLower(summary(&all[i].Content)), q) {
				out = append(out, plain(all[i]))
			}
		}
		return out, nil
	})
}
