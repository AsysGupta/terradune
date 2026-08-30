// Package server serves the terradune UI: the current graph as JSON, and a
// server-sent-events stream that pushes every rebuild to connected browsers.
package server

import (
	"embed"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/AsysGupta/terradune/internal/graph"
)

//go:embed index.html
var static embed.FS

// State is what the browser renders. On a failed rebuild the previous good
// nodes/edges are kept and Error is set, so the page never goes blank.
type State struct {
	TerraformVersion string       `json:"terraformVersion"`
	GeneratedAt      time.Time    `json:"generatedAt"`
	Error            string       `json:"error,omitempty"`
	Nodes            []graph.Node `json:"nodes"`
	Edges            []graph.Edge `json:"edges"`
	Rebuilding       bool         `json:"rebuilding"`
}

type Server struct {
	mu      sync.Mutex
	current []byte // marshaled State
	state   State
	clients map[chan []byte]bool
}

func New() *Server {
	return &Server{clients: map[chan []byte]bool{}}
}

// SetGraph replaces the graph after a successful rebuild.
func (s *Server) SetGraph(tfVersion string, g *graph.Graph) {
	s.mu.Lock()
	s.state.TerraformVersion = tfVersion
	s.state.Nodes = g.Nodes
	s.state.Edges = g.Edges
	s.state.Error = ""
	s.state.Rebuilding = false
	s.state.GeneratedAt = time.Now()
	s.broadcastLocked()
	s.mu.Unlock()
}

// SetError reports a failed rebuild, keeping the last good graph.
func (s *Server) SetError(msg string) {
	s.mu.Lock()
	s.state.Error = msg
	s.state.Rebuilding = false
	s.state.GeneratedAt = time.Now()
	s.broadcastLocked()
	s.mu.Unlock()
}

// SetRebuilding tells clients a rebuild is in flight.
func (s *Server) SetRebuilding() {
	s.mu.Lock()
	s.state.Rebuilding = true
	s.broadcastLocked()
	s.mu.Unlock()
}

func (s *Server) broadcastLocked() {
	payload, err := json.Marshal(s.state)
	if err != nil {
		return
	}
	s.current = payload
	for ch := range s.clients {
		select {
		case ch <- payload:
		default: // slow client; it will catch up on its next event
		}
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page, _ := static.ReadFile("index.html")
		w.Write(page)
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		payload := s.current
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write(payload)
	})
	mux.HandleFunc("/events", s.handleEvents)
	return mux
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	ch := make(chan []byte, 4)
	s.mu.Lock()
	s.clients[ch] = true
	initial := s.current
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.mu.Unlock()
	}()

	write := func(payload []byte) bool {
		if _, err := w.Write([]byte("data: ")); err != nil {
			return false
		}
		w.Write(payload)
		w.Write([]byte("\n\n"))
		flusher.Flush()
		return true
	}
	if initial != nil && !write(initial) {
		return
	}

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case payload := <-ch:
			if !write(payload) {
				return
			}
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
