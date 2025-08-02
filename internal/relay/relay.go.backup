// Copyright (c) 2024 SMTP Relay Contributors
// Licensed under the MIT License

package relay

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"smtp-relay/internal/config"
	"smtp-relay/internal/storage"

	gosmtp "github.com/emersion/go-smtp"
)

// Relay represents the SMTP relay server
type Relay struct {
	config  *config.Config
	storage storage.Storage
	server  *gosmtp.Server
	mu      sync.RWMutex
	stopCh  chan struct{}
}

// New creates a new SMTP relay
func New(cfg *config.Config, store storage.Storage) (*Relay, error) {
	backend := &Backend{
		config:  cfg,
		storage: store,
	}

	server := gosmtp.NewServer(backend)
	server.Addr = fmt.Sprintf("%s:%d", cfg.Incoming.Host, cfg.Incoming.Port)
	server.Domain = "localhost"
	server.ReadTimeout = 10 * time.Second
	server.WriteTimeout = 10 * time.Second
	server.MaxMessageBytes = 1024 * 1024 * 10 // 10MB
	server.MaxRecipients = 50

	// Configure TLS if enabled
	if cfg.Incoming.TLS.Enabled {
		cert, err := tls.LoadX509KeyPair(cfg.Incoming.TLS.CertFile, cfg.Incoming.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
		}

		server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
	}

	return &Relay{
		config:  cfg,
		storage: store,
		server:  server,
		stopCh:  make(chan struct{}),
	}, nil
}

// Start starts the SMTP relay server
func (r *Relay) Start() error {
	go func() {
		if r.config.Incoming.TLS.Enabled {
			if err := r.server.ListenAndServeTLS(); err != nil {
				log.Printf("SMTP server error: %v", err)
			}
		} else {
			if err := r.server.ListenAndServe(); err != nil {
				log.Printf("SMTP server error: %v", err)
			}
		}
	}()

	return nil
}

// Stop stops the SMTP relay server
func (r *Relay) Stop() error {
	close(r.stopCh)
	return r.server.Close()
}

// Backend implements the SMTP backend
type Backend struct {
	config  *config.Config
	storage storage.Storage
}

// NewSession creates a new SMTP session
func (b *Backend) NewSession(conn *gosmtp.Conn) (gosmtp.Session, error) {
	return &Session{backend: b}, nil
}

// Session represents an SMTP session
type Session struct {
	backend *Backend
	from    string
	to      []string
}

// AuthPlain handles PLAIN authentication
func (s *Session) AuthPlain(username, password string) error {
	if !s.backend.config.Incoming.Auth.Enabled {
		return nil
	}

	if username != s.backend.config.Incoming.Auth.Username || password != s.backend.config.Incoming.Auth.Password {
		return gosmtp.ErrAuthFailed
	}

	return nil
}

// Mail handles the MAIL FROM command
func (s *Session) Mail(from string, opts *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

// Rcpt handles the RCPT TO command
func (s *Session) Rcpt(to string, opts *gosmtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

// Data handles the DATA command
func (s *Session) Data(r io.Reader) error {
	// Read the entire message
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	// Parse headers
	headers := parseHeaders(data)

	// Create message
	msg := &storage.Message{
		ID:       generateID(),
		From:     s.from,
		To:       s.to,
		Headers:  headers,
		Body:     data,
		Received: time.Now(),
		Status:   "received",
	}

	// Store message immediately
	if err := s.backend.storage.Store(msg); err != nil {
		log.Printf("Failed to store message: %v", err)
		return err
	}

	// Log that message was received
	log.Printf("Message %s received from %s to %v", msg.ID, msg.From, msg.To)

	// Start background relay process - don't wait for it
	go func() {
		// Add a small delay to ensure the SMTP response is sent first
		time.Sleep(100 * time.Millisecond)
		s.forwardMessage(msg)
	}()

	return nil
}

// Reset resets the session
func (s *Session) Reset() {
	s.from = ""
	s.to = nil
}

// Logout logs out the session
func (s *Session) Logout() error {
	return nil
}

// forwardMessage forwards a message to the target SMTP server using net/smtp
func (s *Session) forwardMessage(msg *storage.Message) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Panic in forwardMessage for %s: %v", msg.ID, r)
			s.handleForwardError(msg, fmt.Errorf("panic in forwarding: %v", r))
		}
	}()

	// Update status to forwarding
	msg.Status = "forwarding"
	if err := s.backend.storage.Update(msg.ID, msg); err != nil {
		log.Printf("Failed to update message status to forwarding: %v", err)
	}

	log.Printf("Starting to forward message %s to %s:%d", msg.ID, s.backend.config.Outgoing.Host, s.backend.config.Outgoing.Port)

	addr := fmt.Sprintf("%s:%d", s.backend.config.Outgoing.Host, s.backend.config.Outgoing.Port)

	// Create authentication if required
	var auth smtp.Auth
	if s.backend.config.Outgoing.Auth.Enabled {
		switch s.backend.config.Outgoing.Auth.Method {
		case "plain":
			auth = smtp.PlainAuth("", s.backend.config.Outgoing.Auth.Username, s.backend.config.Outgoing.Auth.Password, s.backend.config.Outgoing.Host)
		case "login":
			// LoginAuth is not available in older Go versions, use PlainAuth instead
			auth = smtp.PlainAuth("", s.backend.config.Outgoing.Auth.Username, s.backend.config.Outgoing.Auth.Password, s.backend.config.Outgoing.Host)
		default:
			auth = smtp.PlainAuth("", s.backend.config.Outgoing.Auth.Username, s.backend.config.Outgoing.Auth.Password, s.backend.config.Outgoing.Host)
		}
	}

	// Send email using net/smtp with timeout
	done := make(chan error, 1)
	go func() {
		done <- smtp.SendMail(addr, auth, msg.From, msg.To, msg.Body)
	}()

	// Wait for completion with timeout
	select {
	case err := <-done:
		if err != nil {
			s.handleForwardError(msg, err)
			return
		}
	case <-time.After(30 * time.Second): // 30 second timeout
		s.handleForwardError(msg, fmt.Errorf("forwarding timeout after 30 seconds"))
		return
	}

	// Update status to forwarded
	msg.Status = "forwarded"
	msg.Forwarded = time.Now()
	if err := s.backend.storage.Update(msg.ID, msg); err != nil {
		log.Printf("Failed to update message status to forwarded: %v", err)
	}

	log.Printf("Message %s forwarded successfully", msg.ID)
}

// handleForwardError handles forwarding errors
func (s *Session) handleForwardError(msg *storage.Message, err error) {
	msg.Status = "failed"
	msg.Error = err.Error()
	if updateErr := s.backend.storage.Update(msg.ID, msg); updateErr != nil {
		log.Printf("Failed to update message status to failed: %v", updateErr)
	}
	log.Printf("Failed to forward message %s: %v", msg.ID, err)
}

// Helper functions
func parseHeaders(data []byte) map[string]string {
	headers := make(map[string]string)
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		if line == "" {
			break // End of headers
		}

		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			headers[key] = value
		}
	}

	return headers
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
