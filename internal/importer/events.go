package importer

import (
	"encoding/json"
	"sync"
)

type Event struct {
	JobID      string      `json:"job_id"`
	Type       string      `json:"type"`
	ExternalID string      `json:"external_id,omitempty"`
	Payload    interface{} `json:"payload,omitempty"`
}

type Broker struct {
	mu   sync.Mutex
	subs map[string]map[chan Event]struct{}
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[string]map[chan Event]struct{})}
}

func (b *Broker) Subscribe(jobID string) (<-chan Event, func()) {
	ch := make(chan Event, 32)
	b.mu.Lock()
	if b.subs[jobID] == nil {
		b.subs[jobID] = make(map[chan Event]struct{})
	}
	b.subs[jobID][ch] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		if subs := b.subs[jobID]; subs != nil {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(b.subs, jobID)
			}
		}
		b.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

func (b *Broker) Publish(event Event) {
	if b == nil {
		return
	}
	b.mu.Lock()
	subs := b.subs[event.JobID]
	for ch := range subs {
		select {
		case ch <- event:
		default:
		}
	}
	b.mu.Unlock()
}

func (b *Broker) SubscriberCount(jobID string) int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[jobID])
}

func MarshalEvent(event Event) []byte {
	data, _ := json.Marshal(event)
	return data
}
