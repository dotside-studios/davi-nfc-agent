package ntag424

import (
	"errors"
	"testing"
)

func TestMemoryCounterStore(t *testing.T) {
	var s MemoryCounterStore
	steps := []struct {
		uid   string
		ctr   uint32
		fresh bool
	}{
		{"A", 5, true},
		{"A", 5, false},
		{"A", 4, false},
		{"A", 6, true},
		{"B", 1, true},
	}
	for _, st := range steps {
		got, err := s.Seen(st.uid, st.ctr)
		if err != nil || got != st.fresh {
			t.Errorf("Seen(%s, %d) = %v, %v, want %v", st.uid, st.ctr, got, err, st.fresh)
		}
	}
}

func TestVerifyURLFresh(t *testing.T) {
	url := "https://ntag.nxp.com/424?e=EF963FF7828658A599F3041510671E88&c=94EED9EE65337086"
	keys := Keys{MetaRead: zeroKey, FileRead: zeroKey}

	store := &MemoryCounterStore{}
	tap, err := VerifyURLFresh(url, keys, store)
	if err != nil {
		t.Fatalf("first tap: %v", err)
	}
	if _, err := VerifyURLFresh(url, keys, store); !errors.Is(err, ErrReplay) {
		t.Errorf("replayed tap error = %v, want ErrReplay", err)
	}

	forged := url[:len(url)-1] + "7"
	if _, err := VerifyURLFresh(forged, keys, store); !errors.Is(err, ErrMACMismatch) {
		t.Errorf("forged tap error = %v, want ErrMACMismatch", err)
	}
	if fresh, _ := store.Seen(tap.UIDString(), tap.ReadCounter+1); !fresh {
		t.Error("a forged URL advanced the store")
	}

	if _, err := VerifyURLFresh(url, keys, nil); err == nil {
		t.Error("nil store accepted")
	}
}
