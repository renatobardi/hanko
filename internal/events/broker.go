// Package events is the in-process pub/sub used for live UI/SSE updates.
// Durable facts live in Postgres (run_events, activity_events); the broker only
// fans out notifications after commit. v0.1 is single-instance (ADR-0008).
package events

import (
	"sync"
)

// Message is a notification about something that changed.
type Message struct {
	Topic string // e.g. "run:<id>", "company:<id>"
	Type  string // e.g. "run.event", "run.status", "task.updated", "activity"
	Data  any
}

// Broker fans out messages to subscribers of a topic. Slow subscribers drop messages
// (they must re-read from Postgres); publishers never block.
type Broker struct {
	mu   sync.RWMutex
	subs map[string]map[chan Message]struct{}
}

func NewBroker() *Broker { return &Broker{subs: map[string]map[chan Message]struct{}{}} }

// Subscribe returns a channel for topic and a cancel func that must be called.
func (b *Broker) Subscribe(topic string, buf int) (<-chan Message, func()) {
	if buf <= 0 {
		buf = 64
	}
	ch := make(chan Message, buf)
	b.mu.Lock()
	if b.subs[topic] == nil {
		b.subs[topic] = map[chan Message]struct{}{}
	}
	b.subs[topic][ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs[topic], ch)
			if len(b.subs[topic]) == 0 {
				delete(b.subs, topic)
			}
			b.mu.Unlock()
			close(ch)
		})
	}
}

// Publish delivers m to current subscribers of m.Topic without blocking.
func (b *Broker) Publish(m Message) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[m.Topic] {
		select {
		case ch <- m:
		default:
		}
	}
}

func RunTopic(runID string) string         { return "run:" + runID }
func CompanyTopic(companyID string) string { return "company:" + companyID }

// UserTopic é o tópico dos avisos de uma pessoa numa empresa (toasts por usuário, ADR-0042): só ela assina.
func UserTopic(companyID, userID string) string { return "user:" + companyID + ":" + userID }
