package wa

import (
	"context"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/store"
)

// History and SearchHistory are core.History: the archive read on the
// engine's goroutine, for the agents' door. Nothing is emitted.

var _ core.History = (*Engine)(nil)

// ask runs f on the engine's goroutine and waits for it, unless ctx or the
// core ends first.
func ask[T any](ctx context.Context, e *Engine, f func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	e.post(func() {
		v, err := f()
		ch <- result{v, err}
	})
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

// readable reports whether chat is one the archive may hand out: stored,
// and not locked.
func (e *Engine) readable(chat string) bool {
	if e.dead || e.arch == nil {
		return false
	}
	c, err := e.arch.Chat(chat)
	return err == nil && !c.Locked
}

// plain is m as a record with no blob: its thumbnail stays in the archive.
func (e *Engine) plain(m store.Message) abi.Message {
	if m.Media != nil && len(m.Media.Thumbnail) > 0 {
		md := *m.Media
		md.Thumbnail = nil
		m.Media = &md
	}
	return e.conv().message(nil, m)
}

func (e *Engine) History(ctx context.Context, chat, before string, limit int) ([]abi.Message, bool, error) {
	type page struct {
		messages []abi.Message
		more     bool
	}
	p, err := ask(ctx, e, func() (page, error) {
		if !e.readable(chat) {
			return page{}, core.ErrNoHistory
		}
		stored, more, err := e.arch.Messages(chat, before, limit)
		if err != nil {
			return page{}, err
		}
		out := make([]abi.Message, len(stored))
		for i, m := range stored {
			out[i] = e.plain(m)
		}
		return page{out, more}, nil
	})
	return p.messages, p.more, err
}

func (e *Engine) SearchHistory(ctx context.Context, chat, query string, limit int) ([]abi.Message, error) {
	return ask(ctx, e, func() ([]abi.Message, error) {
		if !e.readable(chat) {
			return nil, core.ErrNoHistory
		}
		hits, err := e.arch.Search(query, chat, limit)
		if err != nil {
			return nil, err
		}
		out := make([]abi.Message, len(hits))
		for i, h := range hits {
			out[i] = e.plain(h.Message)
		}
		return out, nil
	})
}
