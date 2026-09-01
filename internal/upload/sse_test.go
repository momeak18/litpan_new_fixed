package upload

import (
	"strings"
	"testing"
	"time"
)

func TestBroadcastCoalescesBurstUpdates(t *testing.T) {
	m := NewManager(Options{DataDir: t.TempDir()})
	const id = "task-sse"
	m.mu.Lock()
	m.tasks[id] = &taskState{
		Task: Task{
			TaskID:   id,
			FileName: "demo.bin",
			Status:   StatusPending,
			Message:  "等待上传",
		},
		runDone: make(chan struct{}),
	}
	m.mu.Unlock()

	ch := m.Subscribe()
	defer m.Unsubscribe(ch)

	m.broadcast()
	first := waitPayload(t, ch, 100*time.Millisecond)
	if !strings.Contains(string(first), "等待上传") {
		t.Fatalf("first payload=%s", first)
	}

	m.mu.Lock()
	m.tasks[id].Message = "正在上传到网盘"
	m.mu.Unlock()
	m.broadcast()
	m.mu.Lock()
	m.tasks[id].Message = "上传已暂停"
	m.mu.Unlock()
	m.broadcast()

	select {
	case payload := <-ch:
		t.Fatalf("unexpected immediate payload: %s", payload)
	case <-time.After(50 * time.Millisecond):
	}

	second := waitPayload(t, ch, 250*time.Millisecond)
	text := string(second)
	if !strings.Contains(text, "上传已暂停") {
		t.Fatalf("second payload=%s", text)
	}
	if strings.Contains(text, "正在上传到网盘") {
		t.Fatalf("coalesced payload should keep latest state only: %s", text)
	}

	select {
	case payload := <-ch:
		t.Fatalf("unexpected extra payload: %s", payload)
	case <-time.After(180 * time.Millisecond):
	}
}

func waitPayload(t *testing.T, ch <-chan []byte, timeout time.Duration) []byte {
	t.Helper()
	select {
	case payload := <-ch:
		return payload
	case <-time.After(timeout):
		t.Fatalf("wait payload timeout: %s", timeout)
		return nil
	}
}
