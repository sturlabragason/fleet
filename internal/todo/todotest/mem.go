// Package todotest is an in-memory todo.Store for tests (ADR-164).
package todotest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/todo"
)

// Mem keeps pending items and open sessions in memory; Ctx is every
// item's context.
type Mem struct {
	mu       sync.Mutex
	n        int
	items    []todo.Item
	sessions map[string]todo.Item
	// Ended is each closed session's outcome, by agent.
	Ended map[string]string
	Ctx   string
}

// New is an empty store with context "work".
func New() *Mem {
	return &Mem{sessions: map[string]todo.Item{}, Ended: map[string]string{}, Ctx: "work"}
}

func (m *Mem) uuid() string {
	m.n++
	return fmt.Sprintf("%08x-0000", m.n+0xabc0000)
}

// Add stores it as pending.
func (m *Mem) Add(_ context.Context, it todo.Item) (todo.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it.UUID, it.Entry = m.uuid(), time.Now()
	m.items = append(m.items, it)
	return it, nil
}

func (m *Mem) list(keep func(todo.Item) bool) []todo.Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []todo.Item
	for _, i := range m.items {
		if keep(i) {
			out = append(out, i)
		}
	}
	return out
}

// Admiral lists the admiral's items.
func (m *Mem) Admiral(_ context.Context, n string) ([]todo.Item, error) {
	return m.list(func(i todo.Item) bool { return i.Admiral == n }), nil
}

// Queue lists the agent's items.
func (m *Mem) Queue(_ context.Context, n string) ([]todo.Item, error) {
	return m.list(func(i todo.Item) bool { return i.Queue == n }), nil
}

// Queues counts every agent's items.
func (m *Mem) Queues(context.Context) (map[string]int, error) {
	n := map[string]int{}
	for _, i := range m.list(func(i todo.Item) bool { return i.Queue != "" }) {
		n[i.Queue]++
	}
	return n, nil
}

// Done drops the item whose uuid starts with ref.
func (m *Mem) Done(_ context.Context, ref string) (todo.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, i := range m.items {
		if strings.HasPrefix(i.UUID, ref) {
			m.items = append(m.items[:k], m.items[k+1:]...)
			return i, nil
		}
	}
	return todo.Item{}, fmt.Errorf("no item %q", ref)
}

// Hand moves the agent's items to leader, else to admiral as followups.
func (m *Mem) Hand(_ context.Context, agent, leader, admiral string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k := range m.items {
		if m.items[k].Queue != agent {
			continue
		}
		n++
		if leader != "" {
			m.items[k].Queue = leader
			continue
		}
		m.items[k].Queue, m.items[k].Admiral, m.items[k].Kind = "", admiral, todo.Followup
	}
	return n, nil
}

// Open starts the agent's session unless one is open.
func (m *Mem) Open(_ context.Context, s todo.Session) (todo.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if it, ok := m.sessions[s.Agent]; ok {
		return it, nil
	}
	it := todo.Item{UUID: m.uuid(), Description: s.Goal, Entry: time.Now(), Ctx: s.Ctx, Kind: todo.SessionKind}
	m.sessions[s.Agent] = it
	return it, nil
}

// SessionOf is the agent's open session.
func (m *Mem) SessionOf(_ context.Context, agent string) (todo.Item, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.sessions[agent]
	return it, ok, nil
}

// End closes the agent's session with outcome.
func (m *Mem) End(_ context.Context, agent, outcome string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[agent]; ok {
		delete(m.sessions, agent)
		m.Ended[agent] = outcome
	}
	return nil
}

// Context is m.Ctx for every dir.
func (m *Mem) Context(string) string { return m.Ctx }
