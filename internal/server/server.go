// Package server serves the terradune UI: the current graphs as JSON, and a
// server-sent-events stream that pushes every rebuild to connected browsers.
package server

import (
	"embed"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/AsysGupta/terradune/internal/graph"
)

//go:embed index.html assets
var static embed.FS

// Workspace is one Terraform working directory's contribution to the page.
// A failed re-plan keeps the previous nodes and edges and sets Error, so the
// page never goes blank.
type Workspace struct {
	Name             string       `json:"name"`
	Dir              string       `json:"dir"`
	TerraformVersion string       `json:"terraformVersion,omitempty"`
	Error            string       `json:"error,omitempty"`
	Rebuilding       bool         `json:"rebuilding"`
	Nodes            []graph.Node `json:"nodes"`
	Edges            []graph.Edge `json:"edges"`
}

// State is the whole page: every workspace under the scanned root.
type State struct {
	Root        string      `json:"root"`
	GeneratedAt time.Time   `json:"generatedAt"`
	Workspaces  []Workspace `json:"workspaces"`
}

type Server struct {
	mu         sync.Mutex
	root       string
	workspaces map[string]*Workspace
	current    []byte
	clients    map[chan []byte]bool
}

func New(root string) *Server {
	return &Server{
		root:       root,
		workspaces: map[string]*Workspace{},
		clients:    map[chan []byte]bool{},
	}
}

func (s *Server) get(name, dir string) *Workspace {
	ws, ok := s.workspaces[name]
	if !ok {
		ws = &Workspace{Name: name, Dir: dir}
		s.workspaces[name] = ws
	}
	return ws
}

// SetGraph records a successful plan for one workspace.
func (s *Server) SetGraph(name, dir, tfVersion string, g *graph.Graph) {
	s.mu.Lock()
	ws := s.get(name, dir)
	ws.TerraformVersion = tfVersion
	ws.Nodes, ws.Edges = g.Nodes, g.Edges
	ws.Error, ws.Rebuilding = "", false
	s.broadcastLocked()
	s.mu.Unlock()
}

// SetError records a failed plan, keeping that workspace's last good graph.
func (s *Server) SetError(name, dir, msg string) {
	s.mu.Lock()
	ws := s.get(name, dir)
	ws.Error, ws.Rebuilding = msg, false
	s.broadcastLocked()
	s.mu.Unlock()
}

// SetRebuilding marks one workspace as re-planning.
func (s *Server) SetRebuilding(name, dir string) {
	s.mu.Lock()
	s.get(name, dir).Rebuilding = true
	s.broadcastLocked()
	s.mu.Unlock()
}

func (s *Server) broadcastLocked() {
	state := State{Root: s.root, GeneratedAt: time.Now()}
	for _, ws := range s.workspaces {
		state.Workspaces = append(state.Workspaces, *ws)
	}
	sort.Slice(state.Workspaces, func(i, j int) bool {
		return state.Workspaces[i].Name < state.Workspaces[j].Name
	})
	payload, err := json.Marshal(state)
	if err != nil {
		return
	}
	s.current = payload
	for ch := range s.clients {
		select {
		case ch <- payload:
		default: // slow client; it catches up on the next event
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
	mux.Handle("/assets/", http.FileServer(http.FS(static)))
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
