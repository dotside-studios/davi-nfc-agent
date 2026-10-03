package nfc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Raw session lease bounds. A lease keeps the reader to one client so a
// multi-step exchange, such as an authentication, is not disturbed by polling or
// by another operation.
const (
	DefaultRawSessionTTL = 5 * time.Second
	MaxRawSessionTTL     = 30 * time.Second
)

// rawLease is the reader held for one client's exchange. It owns the reader's
// operation slot from begin until it ends, which is what makes other operations
// wait.
type rawLease struct {
	id   string
	uid  string
	tag  Tag
	ttl  time.Duration
	done chan struct{}

	exec sync.Mutex

	mu      sync.Mutex
	expires time.Time
	ended   bool
}

func (l *rawLease) refresh(now time.Time) {
	l.mu.Lock()
	l.expires = now.Add(l.ttl)
	l.mu.Unlock()
}

func newLeaseID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func clampRawSessionTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return DefaultRawSessionTTL
	}
	if ttl > MaxRawSessionTTL {
		return MaxRawSessionTTL
	}
	return ttl
}

// BeginRawSession takes the reader for one client, refusing unless the tag
// present is the one expectUID names. While the lease is held polling sends
// nothing to the card and other tag operations wait for the reader. It ends when
// EndRawSession is called, after ttl without an exchange, or when the card
// leaves. A ttl of zero selects DefaultRawSessionTTL and is capped at
// MaxRawSessionTTL.
func (r *deviceReader) BeginRawSession(ctx context.Context, expectUID string, ttl time.Duration) (string, error) {
	ttl = clampRawSessionTTL(ttl)

	if err := r.acquireSlot(ctx); err != nil {
		return "", err
	}

	lease, err := r.openLease(expectUID, ttl)
	if err != nil {
		<-r.opSlot
		return "", err
	}
	return lease.id, nil
}

func (r *deviceReader) openLease(expectUID string, ttl time.Duration) (*rawLease, error) {
	r.opsMu.Lock()
	if r.opsClosed {
		r.opsMu.Unlock()
		return nil, fmt.Errorf("nfc: reader is stopping")
	}
	r.opsWg.Add(1)
	r.opsMu.Unlock()

	tag, err := r.soleTag(expectUID)
	if err == nil && !CanTagTransceive(tag) {
		err = NewNotSupportedError("Transceive")
	}
	if err == nil {
		if _, ok := tag.(TagTransceiver); !ok {
			err = NewNotSupportedError("Transceive")
		}
	}
	id := ""
	if err == nil {
		id, err = newLeaseID()
	}
	if err != nil {
		r.opsWg.Done()
		return nil, err
	}

	lease := &rawLease{
		id:      id,
		uid:     tag.UID(),
		tag:     tag,
		ttl:     ttl,
		done:    make(chan struct{}),
		expires: r.clock.Now().Add(ttl),
	}
	r.leaseMu.Lock()
	r.lease = lease
	r.leaseMu.Unlock()

	go r.watchLease(lease)
	return lease, nil
}

// watchLease ends the lease once its time to live passes without an exchange.
func (r *deviceReader) watchLease(l *rawLease) {
	defer r.opsWg.Done()
	for {
		l.mu.Lock()
		remaining := l.expires.Sub(r.clock.Now())
		l.mu.Unlock()
		if remaining <= 0 {
			l.exec.Lock()
			l.mu.Lock()
			expired := !l.expires.After(r.clock.Now())
			l.mu.Unlock()
			l.exec.Unlock()
			if expired {
				r.endLease(l)
				return
			}
			continue
		}
		select {
		case <-l.done:
			return
		case <-r.stopChan:
			r.endLease(l)
			return
		case <-r.clock.After(remaining):
		}
	}
}

// endLease releases the reader. Safe to call more than once.
func (r *deviceReader) endLease(l *rawLease) bool {
	l.mu.Lock()
	if l.ended {
		l.mu.Unlock()
		return false
	}
	l.ended = true
	close(l.done)
	l.mu.Unlock()

	r.leaseMu.Lock()
	if r.lease == l {
		r.lease = nil
	}
	r.leaseMu.Unlock()

	<-r.opSlot
	return true
}

func (r *deviceReader) leaseByID(id string) (*rawLease, error) {
	r.leaseMu.Lock()
	l := r.lease
	r.leaseMu.Unlock()
	if l == nil || l.id != id {
		return nil, NewRawSessionExpiredError("RawSession")
	}
	return l, nil
}

// leaseUID reports the UID of the tag a lease is held on, with false when none
// is held.
func (r *deviceReader) leaseUID() (string, bool) {
	r.leaseMu.Lock()
	defer r.leaseMu.Unlock()
	if r.lease == nil {
		return "", false
	}
	return r.lease.uid, true
}

// EndRawSession releases a lease. An unknown or already ended lease is refused
// with a raw-session-expired error.
func (r *deviceReader) EndRawSession(leaseID string) error {
	l, err := r.leaseByID(leaseID)
	if err != nil {
		return err
	}
	if !r.endLease(l) {
		return NewRawSessionExpiredError("RawSession")
	}
	return nil
}

// TransceiveInSession exchanges raw bytes with the tag the lease was begun on,
// without taking the reader's operation slot, which the lease holds. Each
// exchange renews the lease. A card that has left ends it.
func (r *deviceReader) TransceiveInSession(ctx context.Context, leaseID string, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("no command bytes to send")
	}
	l, err := r.leaseByID(leaseID)
	if err != nil {
		return nil, err
	}

	l.exec.Lock()
	defer l.exec.Unlock()

	l.mu.Lock()
	ended := l.ended
	l.mu.Unlock()
	if ended {
		return nil, NewRawSessionExpiredError("RawSession")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	resp, err := l.tag.(TagTransceiver).Transceive(data)
	if err != nil {
		if IsCardRemovedError(err) || IsTagRemovedError(err) {
			r.endLease(l)
		}
		return nil, err
	}
	l.refresh(r.clock.Now())
	return resp, nil
}

func (r *deviceReader) endAnyLease() {
	r.leaseMu.Lock()
	l := r.lease
	r.leaseMu.Unlock()
	if l != nil {
		r.endLease(l)
	}
}
