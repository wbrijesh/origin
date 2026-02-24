package http

import (
	"net/http"
)

// Page handlers — stubbed out for Phase 6.

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Origin — home (coming in Phase 6)")) //nolint:errcheck
}

func (s *Server) handleRepo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("repo: " + r.PathValue("repo"))) //nolint:errcheck
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("tree")) //nolint:errcheck
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("blob")) //nolint:errcheck
}

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("log")) //nolint:errcheck
}

func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("commit")) //nolint:errcheck
}

func (s *Server) handleRefs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("refs")) //nolint:errcheck
}

func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("archive")) //nolint:errcheck
}
