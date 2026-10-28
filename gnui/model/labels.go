package model

import (
	"bytes"

	"zapfast/abi"
)

// Label is one of the account's labels (WhatsApp Business), its bytes views
// valid until the next apply.
type Label struct {
	ID, Name, Color []byte // Color is "#rrggbb"
	CreatedAt       int64
}

type labelRec struct {
	id, name, color Str
	created         int64
}

// labelList is the account's labels, in the core's order. A reload's chunks
// build the next list, which replaces the shown one at its last chunk.
type labelList struct {
	recs, nrecs []labelRec
	a, na       arena
	batch       uint32
	streaming   bool
	gen         uint32
}

func (l *labelList) chunk(e *abi.EventLabels) {
	if !l.streaming || e.Batch != l.batch {
		l.nrecs, l.na.b, l.na.live = l.nrecs[:0], l.na.b[:0], 0
	}
	l.batch, l.streaming = e.Batch, !e.Last
	for i := range e.Labels {
		x := &e.Labels[i]
		l.nrecs = append(l.nrecs, labelRec{id: l.na.put(x.ID), name: l.na.put(x.Name), color: l.na.put(x.ColorHex), created: x.CreatedAt})
	}
	if e.Last {
		l.recs, l.nrecs = l.nrecs, l.recs[:0]
		l.a, l.na = l.na, l.a
		l.na.b, l.na.live = l.na.b[:0], 0
		l.gen++
	}
}

func (l *labelList) at(i int) Label {
	r := &l.recs[i]
	return Label{ID: l.a.at(r.id), Name: l.a.at(r.name), Color: l.a.at(r.color), CreatedAt: r.created}
}

func (l *labelList) find(id []byte) int {
	for i := range l.recs {
		if bytes.Equal(l.a.at(l.recs[i].id), id) {
			return i
		}
	}
	return -1
}

// Labels is the number of the account's labels.
func (s *Store) Labels() int { return len(s.labels.recs) }

// LabelAt is label i, in the core's order.
func (s *Store) LabelAt(i int) Label { return s.labels.at(i) }

// FindLabel is the index of the label with id, or -1.
func (s *Store) FindLabel(id []byte) int { return s.labels.find(id) }

// LabelGen moves whenever the labels change.
func (s *Store) LabelGen() uint32 { return s.labels.gen }
