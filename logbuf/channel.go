package logbuf

import (
	"io"
	"log"
	"sync/atomic"
)

// installed is where package channels write, besides stderr. It holds the
// leveled writers rather than the ring, so a line does not build one per write
// and lose the partial it was holding.
var installed atomic.Pointer[sinks]

type sinks struct{ info, warn, errors io.Writer }

// Install makes r the ring that [Channel] loggers write into, besides wherever
// the standard logger's output goes. A program showing its log in a console installs the ring
// the console reads:
//
//	logs := logbuf.New(logbuf.DefaultCapacity)
//	logbuf.Install(logs)
//
// A program that shows its log nowhere never calls it, and the channels write
// only where the standard logger writes. Installing nil goes back to that.
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

// channelWriter sends a line where the program's log goes, and to the installed
// ring at its level. Both are read per write, so a channel built at package
// load follows a later [log.SetOutput] and lands in a ring installed after it.
type channelWriter struct{ level Level }

func (w channelWriter) Write(p []byte) (int, error) {
	n, err := log.Default().Writer().Write(p)

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
