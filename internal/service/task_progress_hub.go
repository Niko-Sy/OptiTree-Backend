package service

import (
	"sync"
	"time"
)

// TaskProgressEvent is pushed to websocket subscribers.
type TaskProgressEvent struct {
	Event         string      `json:"event"`
	ProjectID     string      `json:"projectId"`
	TaskID        string      `json:"taskId,omitempty"`
	Status        string      `json:"status,omitempty"`
	ProjectStatus string      `json:"projectStatus,omitempty"`
	Progress      int         `json:"progress,omitempty"`
	Stage         string      `json:"stage,omitempty"`
	StageLabel    string      `json:"stageLabel,omitempty"`
	ErrorMessage  string      `json:"errorMessage,omitempty"`
	Result        interface{} `json:"result,omitempty"`
	UpdatedAt     string      `json:"updatedAt"`
}

func NewTaskProgressEvent(event, projectID string) TaskProgressEvent {
	return TaskProgressEvent{
		Event:     event,
		ProjectID: projectID,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// TaskProgressHub is an in-memory project-scoped pub/sub hub for websocket pushes.
type TaskProgressHub struct {
	mu   sync.RWMutex
	subs map[string]map[chan TaskProgressEvent]struct{}
}

func NewTaskProgressHub() *TaskProgressHub {
	return &TaskProgressHub{
		subs: make(map[string]map[chan TaskProgressEvent]struct{}),
	}
}

func (h *TaskProgressHub) Subscribe(projectID string) (<-chan TaskProgressEvent, func()) {
	ch := make(chan TaskProgressEvent, 32)

	h.mu.Lock()
	if _, ok := h.subs[projectID]; !ok {
		h.subs[projectID] = make(map[chan TaskProgressEvent]struct{})
	}
	h.subs[projectID][ch] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if set, ok := h.subs[projectID]; ok {
			if _, exists := set[ch]; exists {
				delete(set, ch)
				close(ch)
			}
			if len(set) == 0 {
				delete(h.subs, projectID)
			}
		}
	}

	return ch, unsubscribe
}

func (h *TaskProgressHub) Publish(projectID string, evt TaskProgressEvent) {
	h.mu.RLock()
	set, ok := h.subs[projectID]
	if !ok || len(set) == 0 {
		h.mu.RUnlock()
		return
	}
	listeners := make([]chan TaskProgressEvent, 0, len(set))
	for ch := range set {
		listeners = append(listeners, ch)
	}
	h.mu.RUnlock()

	for _, ch := range listeners {
		select {
		case ch <- evt:
		default:
			// Drop slow-client messages to avoid blocking producers.
		}
	}
}
