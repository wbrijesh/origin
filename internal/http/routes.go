package http

import (
	"net/http"
)

// registerRoutes sets up all HTTP routes.
// Internal routes use the /-/ prefix to avoid collisions with repo names.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Static files
	mux.Handle("GET /-/static/", http.StripPrefix("/-/static/", http.FileServer(http.Dir("static"))))

	// Setup (first run)
	mux.HandleFunc("GET /-/setup", s.handleSetup)
	mux.HandleFunc("POST /-/setup", s.handleSetupPost)

	// Auth
	mux.HandleFunc("GET /-/login", s.handleLogin)
	mux.HandleFunc("POST /-/login", s.handleLoginPost)
	mux.HandleFunc("POST /-/logout", s.handleLogout)

	// Server settings (requires auth)
	mux.HandleFunc("GET /-/settings", s.requireAuth(s.handleSettings))
	mux.HandleFunc("POST /-/settings/ssh-keys", s.requireAuth(s.handleAddSSHKey))
	mux.HandleFunc("POST /-/settings/ssh-keys/{id}/delete", s.requireAuth(s.handleDeleteSSHKey))
	mux.HandleFunc("POST /-/settings/tokens", s.requireAuth(s.handleCreateToken))
	mux.HandleFunc("POST /-/settings/tokens/{id}/delete", s.requireAuth(s.handleDeleteToken))
	mux.HandleFunc("POST /-/settings/password", s.requireAuth(s.handleChangePassword))

	// Repo management (requires auth)
	mux.HandleFunc("GET /-/repos/new", s.requireAuth(s.handleNewRepo))
	mux.HandleFunc("POST /-/repos", s.requireAuth(s.handleCreateRepo))

	// Home page
	mux.HandleFunc("GET /{$}", s.handleHome)

	// Git smart HTTP protocol (read-only)
	mux.HandleFunc("GET /{repo}/info/refs", s.gitInfoRefs)
	mux.HandleFunc("POST /{repo}/git-upload-pack", s.gitUploadPack)
	mux.HandleFunc("POST /{repo}/git-receive-pack", s.gitReceivePackDenied)

	// Per-repo settings (requires auth)
	mux.HandleFunc("GET /{repo}/-/settings", s.requireAuth(s.handleRepoSettings))
	mux.HandleFunc("POST /{repo}/-/settings", s.requireAuth(s.handleUpdateRepoSettings))
	mux.HandleFunc("POST /{repo}/-/rename", s.requireAuth(s.handleRenameRepo))
	mux.HandleFunc("POST /{repo}/-/delete", s.requireAuth(s.handleDeleteRepo))
	mux.HandleFunc("POST /{repo}/-/webhooks", s.requireAuth(s.handleAddWebhook))
	mux.HandleFunc("POST /{repo}/-/webhooks/{wid}/delete", s.requireAuth(s.handleDeleteWebhook))

	// Web UI — repo pages
	mux.HandleFunc("GET /{repo}/{$}", s.handleRepo)
	mux.HandleFunc("GET /{repo}/tree/{ref}/{path...}", s.handleTree)
	mux.HandleFunc("GET /{repo}/blob/{ref}/{path...}", s.handleBlob)
	mux.HandleFunc("GET /{repo}/log/{ref}", s.handleLog)
	mux.HandleFunc("GET /{repo}/commit/{hash}", s.handleCommit)
	mux.HandleFunc("GET /{repo}/refs", s.handleRefs)
	mux.HandleFunc("GET /{repo}/archive/{ref}", s.handleArchive)
}
