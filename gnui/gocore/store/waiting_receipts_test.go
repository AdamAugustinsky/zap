package store

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

// zf:src/archive/receipts.rs:58, :151, :229
func TestWaitingReceiptsMergeMoveAndAreBounded(t *testing.T) {
	s, path, key := testStore(t)
	now := Now()
	chat := "1@lid"
	for _, r := range []struct {
		status int
		at     int64
	}{{2, 30}, {1, 20}, {1, 10}, {3, 40}} {
		if err := s.WaitReceipt(chat, "m", chat, r.status, r.at, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.WaitReceipt(chat, "m", chat, 0, 1, now); err == nil {
		t.Fatal("a sent stage is no receipt")
	}
	// The mapping moves them as chat and as recipient.
	if err := s.Canonicalize(chat, "2@s.whatsapp.net"); err != nil {
		t.Fatal(err)
	}
	chat = "2@s.whatsapp.net"
	if ids, err := s.WaitingFiled(chat); err != nil || len(ids) != 0 {
		t.Fatalf("no message filed yet: %v %v", ids, err)
	}
	if err := s.SaveMessages([]Message{{ID: "m", ChatID: chat, FromMe: true, Timestamp: 5}}); err != nil {
		t.Fatal(err)
	}
	if ids, err := s.WaitingFiled(chat); err != nil || !slices.Equal(ids, []string{"m"}) {
		t.Fatalf("filed %v %v", ids, err)
	}
	got, err := s.TakeWaitingReceipts(chat, "m")
	want := []WaitingReceipt{{Sender: chat, Status: 3, DeliveredAt: 10, ReadAt: 30, Played: 40}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("taken %+v %v", got, err)
	}
	if got, _ = s.TakeWaitingReceipts(chat, "m"); len(got) != 0 {
		t.Fatalf("taken twice: %+v", got)
	}

	// Beyond the bound the oldest go first; past their age, all do.
	for n := range WaitingMax + 1 {
		if err := s.WaitReceipt(chat, fmt.Sprint(n), chat, 1, 1, now+int64(n)); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, s, "SELECT count(*) FROM waiting_receipts"); n != WaitingMax {
		t.Fatalf("kept %d", n)
	}
	if n := count(t, s, "SELECT count(*) FROM waiting_receipts WHERE id='0'"); n != 0 {
		t.Fatal("the oldest should have gone")
	}
	if err := s.WaitReceipt(chat, "late", chat, 1, 1, now+1+WaitingMaxAge); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, "SELECT count(*) FROM waiting_receipts"); n != WaitingMax || count(t, s, "SELECT count(*) FROM waiting_receipts WHERE id='1'") != 0 {
		t.Fatalf("one should have aged out: %d", n)
	}
	// Opening drops what waited too long.
	if _, err := s.db.Exec("UPDATE waiting_receipts SET queued=?", now-WaitingMaxAge); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenArchive(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n := count(t, s, "SELECT count(*) FROM waiting_receipts"); n != 0 {
		t.Fatalf("kept %d past their age", n)
	}
}
