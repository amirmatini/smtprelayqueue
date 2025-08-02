// Copyright (c) 2024 SMTP Relay Contributors
// Licensed under the MIT License

package relay

import (
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
	retryCh chan *storage.Message
}

// New creates a new relay instance
func New(cfg *config.Config, store storage.Storage) (*Relay, error) {
	relay := &Relay{
		config:  cfg,
		storage: store,
		stopCh:  make(chan struct{}),
		retryCh: make(chan *storage.Message, cfg.Retry.RetryQueueSize),
	}

	backend := &Backend{
		config:  cfg,
		storage: store,
		relay:   relay,
	}

	server := gosmtp.NewServer(backend)
	server.Addr = fmt.Sprintf("%s:%d", cfg.Incoming.Host, cfg.Incoming.Port)
	server.Domain = cfg.Incoming.Host
	server.ReadTimeout = 10 * time.Second
	server.WriteTimeout = 10 * time.Second
	server.MaxMessageBytes = 1024 * 1024 * 10 // 10MB
	server.MaxRecipients = 50

	relay.server = server

	// Start retry worker if retry is enabled
	if cfg.Retry.Enabled {
		go relay.retryWorker()
	}

	return relay, nil
}

// Start starts the relay server
func (r *Relay) Start() error {
	log.Printf("Starting SMTP relay on %s:%d", r.config.Incoming.Host, r.config.Incoming.Port)
	return r.server.ListenAndServe()
}

// Stop stops the relay server
func (r *Relay) Stop() error {
	close(r.stopCh)
	return r.server.Close()
}

// retryWorker processes failed messages for retry
func (r *Relay) retryWorker() {
	ticker := time.NewTicker(30 * time.Second) // Check for retries every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case msg := <-r.retryCh:
			r.processRetry(msg)
		case <-ticker.C:
			r.checkForRetries()
		}
	}
}

// processRetry attempts to retry a failed message
func (r *Relay) processRetry(msg *storage.Message) {
	if !r.config.Retry.Enabled {
		return
	}

	if msg.RetryAttempt >= r.config.Retry.MaxAttempts {
		log.Printf("Message %s has exceeded max retry attempts (%d), marking as permanently failed", msg.ID, r.config.Retry.MaxAttempts)
		msg.Status = "failed"
		msg.Error = fmt.Sprintf("Exceeded max retry attempts (%d)", r.config.Retry.MaxAttempts)
		r.storage.Update(msg.ID, msg)
		return
	}

	// Check if it's time to retry
	if time.Now().Before(msg.NextRetry) {
		// Put it back in the queue for later
		select {
		case r.retryCh <- msg:
		default:
			log.Printf("Retry queue full, will retry message %s later", msg.ID)
		}
		return
	}

	log.Printf("Retrying message %s (attempt %d/%d)", msg.ID, msg.RetryAttempt+1, r.config.Retry.MaxAttempts)

	// Attempt to forward the message
	success := r.attemptForward(msg)

	if success {
		log.Printf("Message %s retry successful", msg.ID)
	} else {
		// Calculate next retry delay
		delay := r.calculateRetryDelay(msg.RetryAttempt)
		msg.NextRetry = time.Now().Add(delay)
		msg.RetryAttempt++
		msg.Status = "retrying"

		// Add retry attempt to history
		retryAttempt := storage.RetryAttempt{
			Attempt:   msg.RetryAttempt,
			Timestamp: time.Now(),
			Error:     msg.Error,
		}
		msg.RetryHistory = append(msg.RetryHistory, retryAttempt)

		r.storage.Update(msg.ID, msg)

		// Put back in retry queue
		select {
		case r.retryCh <- msg:
		default:
			log.Printf("Retry queue full, will retry message %s later", msg.ID)
		}
	}
}

// checkForRetries checks for messages that are ready to be retried
func (r *Relay) checkForRetries() {
	if !r.config.Retry.Enabled {
		return
	}

	failedMessages, err := r.storage.GetFailedMessages()
	if err != nil {
		log.Printf("Failed to get failed messages for retry: %v", err)
		return
	}

	for _, msg := range failedMessages {
		if msg.Status == "retrying" && time.Now().After(msg.NextRetry) {
			select {
			case r.retryCh <- msg:
			default:
				log.Printf("Retry queue full, skipping message %s", msg.ID)
			}
		}
	}
}

// calculateRetryDelay calculates the delay for the next retry attempt
func (r *Relay) calculateRetryDelay(attempt int) time.Duration {
	delay := r.config.Retry.InitialDelay
	for i := 0; i < attempt; i++ {
		delay = time.Duration(float64(delay) * r.config.Retry.BackoffMultiplier)
		if delay > r.config.Retry.MaxDelay {
			delay = r.config.Retry.MaxDelay
			break
		}
	}
	return delay
}

// attemptForward attempts to forward a message and returns success status
func (r *Relay) attemptForward(msg *storage.Message) bool {
	addr := fmt.Sprintf("%s:%d", r.config.Outgoing.Host, r.config.Outgoing.Port)

	// Create authentication if required
	var auth smtp.Auth
	if r.config.Outgoing.Auth.Enabled {
		switch r.config.Outgoing.Auth.Method {
		case "plain":
			auth = smtp.PlainAuth("", r.config.Outgoing.Auth.Username, r.config.Outgoing.Auth.Password, r.config.Outgoing.Host)
		case "login":
			auth = smtp.PlainAuth("", r.config.Outgoing.Auth.Username, r.config.Outgoing.Auth.Password, r.config.Outgoing.Host)
		default:
			auth = smtp.PlainAuth("", r.config.Outgoing.Auth.Username, r.config.Outgoing.Auth.Password, r.config.Outgoing.Host)
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
			msg.Error = err.Error()
			return false
		}
	case <-time.After(30 * time.Second): // 30 second timeout
		msg.Error = "forwarding timeout after 30 seconds"
		return false
	}

	// Success - update message status
	msg.Status = "forwarded"
	msg.Forwarded = time.Now()
	msg.Error = ""
	r.storage.Update(msg.ID, msg)
	return true
}

// Backend implements the SMTP backend
type Backend struct {
	config  *config.Config
	storage storage.Storage
	relay   *Relay
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
		return fmt.Errorf("invalid credentials")
	}

	return nil
}

// Mail handles the MAIL command
func (s *Session) Mail(from string, opts *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

// Rcpt handles the RCPT command
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
		ID:           generateID(),
		From:         s.from,
		To:           s.to,
		Headers:      headers,
		Body:         data,
		Received:     time.Now(),
		Status:       "received",
		RetryAttempt: 0,
		RetryHistory: []storage.RetryAttempt{},
	}

	// Store message immediately
	if err := s.backend.storage.Store(msg); err != nil {
		log.Printf("Failed to store message: %v", err)
		return err
	}

	// Log that message was received
	log.Printf("Message %s received from %s to %v", msg.ID, msg.From, msg.To)

	// Start background relay process - TRULY asynchronous now
	go s.forwardMessage(msg)

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

// forwardMessage forwards a message to the target SMTP server
func (s *Session) forwardMessage(msg *storage.Message) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Panic in forwardMessage for %s: %v", msg.ID, r)
			s.handleForwardError(msg)
		}
	}()

	// Update status to forwarding
	msg.Status = "forwarding"
	if err := s.backend.storage.Update(msg.ID, msg); err != nil {
		log.Printf("Failed to update message status to forwarding: %v", err)
	}

	log.Printf("Starting to forward message %s to %s:%d", msg.ID, s.backend.config.Outgoing.Host, s.backend.config.Outgoing.Port)

	// Attempt to forward ASYNCHRONOUSLY - don't wait for it
	go func() {
		success := s.backend.relay.attemptForward(msg)

		if !success {
			s.handleForwardError(msg)
		} else {
			log.Printf("Message %s forwarded successfully", msg.ID)
		}
	}()
}

// handleForwardError handles forwarding errors
func (s *Session) handleForwardError(msg *storage.Message) {
	if s.backend.config.Retry.Enabled && msg.RetryAttempt < s.backend.config.Retry.MaxAttempts {
		// Schedule for retry
		msg.Status = "retrying"
		msg.RetryAttempt++
		msg.NextRetry = time.Now().Add(s.backend.config.Retry.InitialDelay)

		// Add retry attempt to history
		retryAttempt := storage.RetryAttempt{
			Attempt:   msg.RetryAttempt,
			Timestamp: time.Now(),
			Error:     msg.Error,
		}
		msg.RetryHistory = append(msg.RetryHistory, retryAttempt)

		log.Printf("Message %s scheduled for retry (attempt %d/%d)", msg.ID, msg.RetryAttempt, s.backend.config.Retry.MaxAttempts)

		// Add to retry queue
		select {
		case s.backend.relay.retryCh <- msg:
		default:
			log.Printf("Retry queue full, will retry message %s later", msg.ID)
		}
	} else {
		// Mark as permanently failed
		msg.Status = "failed"
		log.Printf("Message %s failed permanently after %d attempts", msg.ID, msg.RetryAttempt)
	}

	if updateErr := s.backend.storage.Update(msg.ID, msg); updateErr != nil {
		log.Printf("Failed to update message status: %v", updateErr)
	}
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
