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
	tmpl           *template.Template
	mux            *http.ServeMux
	chatHandler    *ChatHandler
	agentHandler   *AgentHandler   // Phase 2: Agent with tools
	commandHandler *CommandHandler // Slash command handler
	healthHandler  *HealthHandler  // GET /api/health
}

// NewServer creates a new web server with the given handlers.
func NewServer(chatHandler *ChatHandler, agentHandler *AgentHandler, commandHandler *CommandHandler, healthInfo HealthInfo) (*Server, error) {
	tmpl, err := template.ParseFS(content, "templates/index.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		tmpl:           tmpl,
		mux:            http.NewServeMux(),
		chatHandler:    chatHandler,
		agentHandler:   agentHandler,
		commandHandler: commandHandler,
		healthHandler:  NewHealthHandler(healthInfo),
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
	if s.commandHandler != nil {
		s.mux.HandleFunc("/api/command", s.commandHandler.HandleCommand)
	}
	s.mux.HandleFunc("/api/health", s.healthHandler.ServeHTTP)
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

	// Default to localhost to avoid unintentional LAN exposure for a local tool.
	// Override via WEB_HOST env var for container or multi-host deployments.
	host := os.Getenv("WEB_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	addr := host + ":" + port
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

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

	log.Printf("🌐 Pocket-Omega server running at http://%s", addr)
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		log.Println("✅ Server stopped gracefully")
		return nil // Normal shutdown, not an error
	}
	return err
}
