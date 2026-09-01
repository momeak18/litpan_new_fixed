package upload

import (
	"context"
	"encoding/json"
	"time"
)

const broadcastCoalesceInterval = 120 * time.Millisecond

func (m *Manager) Subscribe() chan []byte {
	ch := make(chan []byte, 2)
	m.subMu.Lock()
	m.subs[ch] = struct{}{}
	m.subMu.Unlock()
	return ch
}

func (m *Manager) Unsubscribe(ch chan []byte) {
	m.subMu.Lock()
	delete(m.subs, ch)
	m.subMu.Unlock()
	close(ch)
}

func (m *Manager) SnapshotPayload() []byte {
	tasks := m.List(context.Background(), 0)
	payload, _ := json.Marshal(map[string]any{"tasks": tasks})
	return payload
}

func (m *Manager) broadcast() {
	m.subMu.Lock()
	if len(m.subs) == 0 {
		m.subMu.Unlock()
		return
	}
	if m.broadcastPending {
		m.broadcastDirty = true
		m.subMu.Unlock()
		return
	}
	m.broadcastPending = true
	m.subMu.Unlock()
	m.flushBroadcast()
}

func (m *Manager) flushBroadcast() {
	payload := m.SnapshotPayload()
	m.subMu.Lock()
	if len(m.subs) == 0 {
		m.broadcastPending = false
		m.broadcastDirty = false
		m.subMu.Unlock()
		return
	}
	for ch := range m.subs {
		select {
		case ch <- payload:
		default:
		}
	}
	m.subMu.Unlock()
	time.AfterFunc(broadcastCoalesceInterval, m.finishBroadcastWindow)
}

func (m *Manager) finishBroadcastWindow() {
	m.subMu.Lock()
	if len(m.subs) == 0 {
		m.broadcastPending = false
		m.broadcastDirty = false
		m.subMu.Unlock()
		return
	}
	if !m.broadcastDirty {
		m.broadcastPending = false
		m.subMu.Unlock()
		return
	}
	m.broadcastDirty = false
	m.subMu.Unlock()
	m.flushBroadcast()
}
