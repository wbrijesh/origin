package http

import (
	"net/http"
)

// registerRoutes sets up all HTTP routes.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Static files
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Git smart HTTP protocol (read-only)
	mux.HandleFunc("GET /{repo}/info/refs", s.gitInfoRefs)
	mux.HandleFunc("POST /{repo}/git-upload-pack", s.gitUploadPack)
	mux.HandleFunc("POST /{repo}/git-receive-pack", s.gitReceivePackDenied)

	// Web UI pages (Phase 6)
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("GET /{repo}/{$}", s.handleRepo)
	mux.HandleFunc("GET /{repo}/tree/{ref}/{path...}", s.handleTree)
	mux.HandleFunc("GET /{repo}/blob/{ref}/{path...}", s.handleBlob)
	mux.HandleFunc("GET /{repo}/log/{ref}", s.handleLog)
	mux.HandleFunc("GET /{repo}/commit/{hash}", s.handleCommit)
	mux.HandleFunc("GET /{repo}/refs", s.handleRefs)
	mux.HandleFunc("GET /{repo}/archive/{ref}", s.handleArchive)
}
