package web

import (
	"context"
	"embed"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed templates/index.html
var content embed.FS

// Server holds the HTTP server and its dependencies.
type Server struct {
	tmpl         *template.Template
	mux          *http.ServeMux
	chatHandler  *ChatHandler
	agentHandler *AgentHandler // Phase 2: Agent with tools
}

// NewServer creates a new web server with the given ChatHandler.
func NewServer(chatHandler *ChatHandler, agentHandler *AgentHandler) (*Server, error) {
	tmpl, err := template.ParseFS(content, "templates/index.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		tmpl:         tmpl,
		mux:          http.NewServeMux(),
		chatHandler:  chatHandler,
		agentHandler: agentHandler,
	}
	s.registerRoutes()
	return s, nil
}

// registerRoutes sets up all HTTP routes.
func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/api/chat", s.chatHandler.HandleChat)
	if s.agentHandler != nil {
		s.mux.HandleFunc("/api/agent", s.agentHandler.HandleAgent)
	}
}

// handleIndex serves the main page.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if err := s.tmpl.Execute(w, nil); err != nil {
		log.Printf("[Web] Template render error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Start begins listening on the configured port with graceful shutdown.
// On SIGINT/SIGTERM, it waits up to 10s for in-flight requests to complete,
// ensuring deferred cleanup (e.g. registry.CloseAll) runs reliably.
func (s *Server) Start() error {
	port := os.Getenv("WEB_PORT")
	if port == "" {
		port = "8080"
	}

	addr := ":" + port
	srv := &http.Server{Addr: addr, Handler: s.mux}

	// Graceful shutdown goroutine
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("⚡ Received signal %v, shutting down gracefully...", sig)

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("⚠️  Graceful shutdown error: %v", err)
		}
	}()

	log.Printf("🌐 Pocket-Omega server running at http://localhost%s", addr)
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		log.Println("✅ Server stopped gracefully")
		return nil // Normal shutdown, not an error
	}
	return err
}
