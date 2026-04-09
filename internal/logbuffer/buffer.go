// Package logbuffer provides a thread-safe ring buffer for recent log entries
// and a zerolog hook to capture warn/error messages automatically.
package logbuffer

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Entry is a single captured log line.
type Entry struct {
	Timestamp time.Time         `json:"timestamp"`
	Level     string            `json:"level"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// Buffer is a fixed-size ring buffer of log entries.
type Buffer struct {
	mu      sync.RWMutex
	entries []Entry
	size    int
	pos     int // next write position
	count   int // total entries written (for computing actual length)
}

// New creates a ring buffer that holds up to size entries.
func New(size int) *Buffer {
	return &Buffer{
		entries: make([]Entry, size),
		size:    size,
	}
}

// Add appends an entry, overwriting the oldest if full.
func (b *Buffer) Add(e Entry) {
	b.mu.Lock()
	b.entries[b.pos] = e
	b.pos = (b.pos + 1) % b.size
	b.count++
	b.mu.Unlock()
}

// Entries returns all stored entries in chronological order (oldest first).
func (b *Buffer) Entries() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	n := b.size
	if b.count < b.size {
		n = b.count
	}

	result := make([]Entry, n)
	if b.count < b.size {
		copy(result, b.entries[:n])
	} else {
		// pos points to the oldest entry (it's the next to be overwritten)
		copy(result, b.entries[b.pos:])
		copy(result[b.size-b.pos:], b.entries[:b.pos])
	}
	return result
}

// Hook is a zerolog.Hook that captures log entries at or above a minimum level.
type Hook struct {
	buf      *Buffer
	minLevel zerolog.Level
}

// NewHook creates a zerolog hook that writes entries at minLevel or above to buf.
func NewHook(buf *Buffer, minLevel zerolog.Level) *Hook {
	return &Hook{buf: buf, minLevel: minLevel}
}

// Run implements zerolog.Hook.
func (h *Hook) Run(e *zerolog.Event, level zerolog.Level, msg string) {
	if level < h.minLevel {
		return
	}

	entry := Entry{
		Timestamp: time.Now().UTC(),
		Level:     level.String(),
		Message:   msg,
	}

	// Extract fields from the event by marshaling it.
	// zerolog doesn't expose fields directly, so we use the event's
	// internal buffer via MarshalZerologObject indirectly.
	// Instead, we capture fields by temporarily marshaling.
	// Since zerolog doesn't give us easy field access in hooks,
	// we rely on the message and level which are the most useful parts.

	h.buf.Add(entry)
}

// HookWithFields is an alternative approach: a zerolog writer that parses
// JSON output and captures fields. Use this instead of Hook when you want
// full field capture.
type HookWriter struct {
	buf      *Buffer
	minLevel zerolog.Level
	inner    zerolog.LevelWriter
}

// NewHookWriter wraps an existing writer and tees warn/error entries to the buffer.
func NewHookWriter(buf *Buffer, minLevel zerolog.Level, inner zerolog.LevelWriter) *HookWriter {
	return &HookWriter{buf: buf, minLevel: minLevel, inner: inner}
}

// Write implements io.Writer.
func (w *HookWriter) Write(p []byte) (n int, err error) {
	return w.inner.Write(p)
}

// WriteLevel implements zerolog.LevelWriter.
func (w *HookWriter) WriteLevel(level zerolog.Level, p []byte) (n int, err error) {
	n, err = w.inner.WriteLevel(level, p)

	if level >= w.minLevel {
		var parsed map[string]interface{}
		if jsonErr := json.Unmarshal(p, &parsed); jsonErr == nil {
			entry := Entry{
				Timestamp: time.Now().UTC(),
				Level:     level.String(),
			}
			if msg, ok := parsed["message"].(string); ok {
				entry.Message = msg
			}
			fields := make(map[string]string)
			for k, v := range parsed {
				switch k {
				case "level", "time", "message":
					continue
				default:
					if s, ok := v.(string); ok {
						fields[k] = s
					} else {
						b, _ := json.Marshal(v)
						fields[k] = string(b)
					}
				}
			}
			if len(fields) > 0 {
				entry.Fields = fields
			}
			w.buf.Add(entry)
		}
	}

	return n, err
}
