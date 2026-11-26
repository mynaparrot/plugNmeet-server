package mcp

import "testing"

func TestExecutorReleaseSessionDropsTheSessionLock(t *testing.T) {
	e := NewExecutor(ExecutorOptions{Registry: NewRegistry()})

	e.lockSession("session-1")()

	e.mu.Lock()
	held := len(e.sessions)
	e.mu.Unlock()
	if held != 1 {
		t.Fatalf("held sessions = %d, want 1", held)
	}

	// An empty id is never a session, and it must not touch the others.
	e.ReleaseSession("")
	e.ReleaseSession("session-1")

	e.mu.Lock()
	held = len(e.sessions)
	e.mu.Unlock()
	if held != 0 {
		t.Errorf("held sessions = %d, want the finished session released", held)
	}
}
