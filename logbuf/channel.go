package logbuf

import (
	"io"
	"log"
	"os"
	"sync/atomic"
)

// installed is where package channels write, besides stderr. It holds the
// leveled writers rather than the ring, so a line does not build one per write
// and lose the partial it was holding.
var installed atomic.Pointer[sinks]

type sinks struct{ info, warn, errors io.Writer }

// Install makes r the ring that [Channel] loggers write into, alongside the
// process's stderr. A program showing its log in a console installs the ring
// the console reads:
//
//	logs := logbuf.New(logbuf.DefaultCapacity)
//	logbuf.Install(logs)
//
// A program that shows its log nowhere never calls it, and the channels write
// to stderr alone. Installing nil goes back to that.
//
// It is process-wide, as the standard logger's own output is. Call it before
// the parts that log start running.
func Install(r *Ring) {
	if r == nil {
		installed.Store(nil)
		return
	}
	installed.Store(&sinks{
		info:   r.At(LevelInfo),
		warn:   r.At(LevelWarn),
		errors: r.At(LevelError),
	})
}

// Channel returns the logger one part of the program reports on, writing under
// name at level:
//
//	var (
//		logf  = logbuf.Channel("device", logbuf.LevelInfo)
//		failf = logbuf.Channel("device", logbuf.LevelError)
//	)
//
// name is what a line is recorded under as [Entry.Source] and what the console
// shows beside it, so it is declared once here rather than written into every
// call. level is the severity of what the channel carries, stated rather than
// read back off the text.
func Channel(name string, level Level) *log.Logger {
	return log.New(channelWriter{level: level}, "["+name+"] ", log.LstdFlags)
}

// channelWriter sends a line to stderr and to the installed ring at its level.
// The ring is read per write, so a channel built before Install still lands in
// it, which is what a package-level channel needs.
type channelWriter struct{ level Level }

func (w channelWriter) Write(p []byte) (int, error) {
	n, err := os.Stderr.Write(p)

	if s := installed.Load(); s != nil {
		if to := s.at(w.level); to != nil {
			_, _ = to.Write(p)
		}
	}
	return n, err
}

func (s *sinks) at(level Level) io.Writer {
	switch level {
	case LevelError:
		return s.errors
	case LevelWarn:
		return s.warn
	default:
		return s.info
	}
}
