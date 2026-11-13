package logging

import (
	"context"
	"sync"
)

type rpcNoteKey struct{}

type RPCNote struct {
	mu   sync.Mutex
	text string
}

func BeginRPCNote(ctx context.Context) (context.Context, *RPCNote) {
	if ctx == nil {
		ctx = context.Background()
	}
	note := &RPCNote{}
	return context.WithValue(ctx, rpcNoteKey{}, note), note
}

func NoteRPCFailure(ctx context.Context, text string) {
	if ctx == nil || text == "" {
		return
	}
	note, _ := ctx.Value(rpcNoteKey{}).(*RPCNote)
	if note == nil {
		return
	}
	const maxNote = 2048
	if len(text) > maxNote {
		text = text[:maxNote]
	}
	note.mu.Lock()
	defer note.mu.Unlock()
	if note.text == "" {
		note.text = text
	}
}

func (n *RPCNote) Text() string {
	if n == nil {
		return ""
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.text
}
