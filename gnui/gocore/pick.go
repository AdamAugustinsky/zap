package gocore

import (
	"context"
	"sync"
	"sync/atomic"

	"zapfast/abi"
	"zapfast/gocore/out"
)

// The bounds of a PickRequest's fields (zf.schema), which the broker cuts
// to rather than drop the request.
const (
	pickTitleMax   = 1024
	pickFilters    = 16
	pickNameMax    = 256
	pickExtensions = 32
	pickExtMax     = 32
	pickFileMax    = 1024
	pickDirMax     = 4096
)

// nextPick numbers dialogs across every Conn of the process, as upstream's
// pick::NEXT does.
var nextPick atomic.Uint64

// picks is the broker between Host.Pick and the UI's PickAnswer (upstream's
// pick::Dialogs): each request waits for its answer until the Conn closes,
// when every one still waiting, and every one asked afterwards, gets no
// paths.
type picks struct {
	mu      sync.Mutex
	closed  bool
	waiting map[uint64]chan []string
}

func (p *picks) ask(ctx context.Context, c *Conn, req *abi.EventPickRequest) []string {
	id := nextPick.Add(1)
	answer := make(chan []string, 1)
	p.mu.Lock()
	// Checked under the lock close takes, so a dialog racing with Close is
	// either answered with the rest or never waits.
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	if p.waiting == nil {
		p.waiting = map[uint64]chan []string{}
	}
	p.waiting[id] = answer
	p.mu.Unlock()
	defer p.forget(id)

	ev := abi.Event{Tag: abi.EventTagPickRequest, PickRequest: bounded(req)}
	ev.PickRequest.ID = id
	// Should it still not encode, the dialog is cancelled for the reader
	// instead of blocking its goroutine for good.
	if !c.out.Emit(&ev) {
		return nil
	}
	select {
	case paths := <-answer:
		return paths
	case <-ctx.Done():
		return nil
	}
}

func (p *picks) forget(id uint64) {
	p.mu.Lock()
	delete(p.waiting, id)
	p.mu.Unlock()
}

// answer answers request id with paths (copied: the command's bytes are
// the caller's). An unknown or already answered id is ignored.
func (p *picks) answer(id uint64, paths [][]byte) {
	p.mu.Lock()
	ch := p.waiting[id]
	delete(p.waiting, id)
	p.mu.Unlock()
	if ch == nil {
		return
	}
	var out []string
	for _, b := range paths {
		out = append(out, string(b))
	}
	ch <- out
}

// close answers every waiting request with no paths, and every later one
// at once.
func (p *picks) close() {
	p.mu.Lock()
	p.closed = true
	waiting := p.waiting
	p.waiting = nil
	p.mu.Unlock()
	for _, ch := range waiting {
		close(ch)
	}
}

// bounded is req with its text cut to the schema's bounds and what cannot
// be carried left off.
func bounded(req *abi.EventPickRequest) abi.EventPickRequest {
	r := abi.EventPickRequest{
		Kind:        req.Kind,
		Title:       clip(req.Title, pickTitleMax),
		HasFileName: req.HasFileName,
	}
	if r.HasFileName {
		r.FileName = clip(req.FileName, pickFileMax)
	}
	if req.HasDirectory && len(req.Directory) <= pickDirMax {
		r.Directory, r.HasDirectory = req.Directory, true
	}
	for _, f := range req.Filters[:min(len(req.Filters), pickFilters)] {
		pf := abi.PickFilter{Name: clip(f.Name, pickNameMax)}
		for _, e := range f.Extensions[:min(len(f.Extensions), pickExtensions)] {
			pf.Extensions = append(pf.Extensions, clip(e, pickExtMax))
		}
		r.Filters = append(r.Filters, pf)
	}
	return r
}

func clip(b []byte, max int) []byte { return []byte(out.Clip(string(b), max)) }
