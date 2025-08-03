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
	"time"

	"smtp-relay/internal/config"
	"smtp-relay/internal/storage"

	"sync/atomic"

	gosmtp "github.com/emersion/go-smtp"
)

// Relay represents the SMTP relay server
type Relay struct {
	config  *config.Config
	storage storage.Storage
	server  *gosmtp.Server
	stopCh  chan struct{}
	retryCh chan *storage.Message
	// Health monitoring
	activeConnections int32
	lastActivity      int64 // Use atomic int64 for thread safety
	healthTicker      *time.Ticker
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

	// Add connection limits and timeouts to prevent hanging
	server.MaxLineLength = 1000                          // Limit line length
	server.AllowInsecureAuth = !cfg.Incoming.TLS.Enabled // Only allow insecure auth if TLS is disabled

	// Configure TLS for incoming server if enabled
	if cfg.Incoming.TLS.Enabled {
		if cfg.Incoming.TLS.CertFile == "" || cfg.Incoming.TLS.KeyFile == "" {
			return nil, fmt.Errorf("TLS enabled but cert_file and key_file are required")
		}

		cert, err := tls.LoadX509KeyPair(cfg.Incoming.TLS.CertFile, cfg.Incoming.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
		}

		server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
	}

	relay.server = server

	// Start health monitoring
	atomic.StoreInt64(&relay.lastActivity, time.Now().Unix())
	relay.healthTicker = time.NewTicker(30 * time.Second)
	go relay.healthMonitor()

	// Start retry worker if retry is enabled
	if cfg.Retry.Enabled {
		// Load retry queue from persistent storage
		if err := relay.loadRetryQueue(); err != nil {
			log.Printf("Warning: Failed to load retry queue: %v", err)
		}
		go relay.retryWorker()
	}

	return relay, nil
}

// healthMonitor monitors server health and prevents hanging
func (r *Relay) healthMonitor() {
	defer r.healthTicker.Stop()

	for {
		select {
		case <-r.stopCh:
			log.Println("Health monitor shutting down")
			return
		case <-r.healthTicker.C:
			r.checkHealth()
		}
	}
}

// checkHealth performs health checks and logs status
func (r *Relay) checkHealth() {
	active := atomic.LoadInt32(&r.activeConnections)
	lastActivity := atomic.LoadInt64(&r.lastActivity)

	// Log health status every 5 minutes
	if time.Since(time.Unix(lastActivity, 0)) > 5*time.Minute {
		log.Printf("Health check: Active connections: %d, Last activity: %v ago",
			active, time.Since(time.Unix(lastActivity, 0)))
	}

	// Update last activity if there are active connections
	if active > 0 {
		atomic.StoreInt64(&r.lastActivity, time.Now().Unix())
	}

	// Log warning if too many connections
	if active > 50 {
		log.Printf("Warning: High number of active connections: %d", active)
	}

	// Deadlock detection: if no activity for too long with active connections
	if active > 0 && time.Since(time.Unix(lastActivity, 0)) > 10*time.Minute {
		log.Printf("WARNING: Potential deadlock detected! %d active connections but no activity for %v",
			active, time.Since(time.Unix(lastActivity, 0)))
	}
}

// loadRetryQueue loads the retry queue from persistent storage
func (r *Relay) loadRetryQueue() error {
	// Add timeout to prevent hanging
	done := make(chan error, 1)
	go func() {
		done <- r.performLoadRetryQueue()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout loading retry queue after 30 seconds")
	}
}

// performLoadRetryQueue performs the actual load operation
func (r *Relay) performLoadRetryQueue() error {
	messages, err := r.storage.LoadRetryQueue()
	if err != nil {
		return fmt.Errorf("failed to load retry queue: %w", err)
	}

	if len(messages) > 0 {
		log.Printf("Loading %d messages from retry queue", len(messages))

		for _, msg := range messages {
			// Check if message is still valid for retry
			if msg.Status == "retrying" && msg.RetryAttempt < r.config.Retry.MaxAttempts {
				// Check if it's time to retry
				if time.Now().After(msg.NextRetry) {
					select {
					case r.retryCh <- msg:
						log.Printf("Queued message %s for immediate retry", msg.ID)
					default:
						log.Printf("Retry queue full, skipping message %s", msg.ID)
					}
				} else {
					// Put it back in queue for later retry
					select {
					case r.retryCh <- msg:
						log.Printf("Queued message %s for retry at %s", msg.ID, msg.NextRetry.Format(time.RFC3339))
					default:
						log.Printf("Retry queue full, skipping message %s", msg.ID)
					}
				}
			} else {
				log.Printf("Skipping message %s (status: %s, attempts: %d)", msg.ID, msg.Status, msg.RetryAttempt)
			}
		}
	}

	return nil
}

// saveRetryQueue saves the current retry queue to persistent storage
func (r *Relay) saveRetryQueue() error {
	// Add timeout to prevent hanging
	done := make(chan error, 1)
	go func() {
		done <- r.performSaveRetryQueue()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout saving retry queue after 30 seconds")
	}
}

// performSaveRetryQueue performs the actual save operation
func (r *Relay) performSaveRetryQueue() error {
	// Get all messages currently in the retry queue
	var queueMessages []*storage.Message

	// We need to collect messages from the channel
	// Since we can't peek into a channel, we'll get all retrying messages from storage
	messages, err := r.storage.GetFailedMessages()
	if err != nil {
		return fmt.Errorf("failed to get failed messages for queue save: %w", err)
	}

	// Filter only retrying messages
	for _, msg := range messages {
		if msg.Status == "retrying" && msg.RetryAttempt < r.config.Retry.MaxAttempts {
			queueMessages = append(queueMessages, msg)
		}
	}

	// Save to persistent storage
	if err := r.storage.SaveRetryQueue(queueMessages); err != nil {
		return fmt.Errorf("failed to save retry queue: %w", err)
	}

	if len(queueMessages) > 0 {
		log.Printf("Saved %d messages to retry queue", len(queueMessages))
	}

	return nil
}

// cleanupOldFailedMessages removes old failed messages to prevent storage bloat
func (r *Relay) cleanupOldFailedMessages() error {
	if r.config.Retry.CleanupFailedAfter <= 0 {
		return nil // Cleanup disabled
	}

	// Add timeout to prevent hanging
	done := make(chan error, 1)
	go func() {
		done <- r.performCleanupOldFailedMessages()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(60 * time.Second):
		return fmt.Errorf("timeout cleaning up old failed messages after 60 seconds")
	}
}

// performCleanupOldFailedMessages performs the actual cleanup operation
func (r *Relay) performCleanupOldFailedMessages() error {
	cutoffTime := time.Now().Add(-r.config.Retry.CleanupFailedAfter)

	// Get all failed messages
	messages, err := r.storage.GetFailedMessages()
	if err != nil {
		return fmt.Errorf("failed to get failed messages for cleanup: %w", err)
	}

	cleaned := 0
	for _, msg := range messages {
		if msg.Status == "failed" && msg.Received.Before(cutoffTime) {
			if err := r.storage.Delete(msg.ID); err != nil {
				log.Printf("Failed to delete old failed message %s: %v", msg.ID, err)
			} else {
				cleaned++
			}
		}
	}

	if cleaned > 0 {
		log.Printf("Cleaned up %d old failed messages (older than %v)", cleaned, r.config.Retry.CleanupFailedAfter)
	}

	return nil
}

// Start starts the relay server
func (r *Relay) Start() error {
	log.Printf("Starting SMTP relay on %s:%d", r.config.Incoming.Host, r.config.Incoming.Port)

	// Add panic recovery
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("Panic in SMTP server: %v", rec)
		}
	}()

	return r.server.ListenAndServe()
}

// Stop stops the relay server with graceful shutdown
func (r *Relay) Stop() error {
	log.Println("Starting graceful shutdown...")

	// Stop health monitoring
	if r.healthTicker != nil {
		r.healthTicker.Stop()
	}

	// Signal shutdown to all workers
	close(r.stopCh)

	// Wait for retry worker to finish processing queue
	if r.config.Retry.Enabled {
		log.Println("Waiting for retry queue to empty...")
		r.waitForRetryQueueEmpty()
	}

	// Wait for active connections to close (with timeout)
	r.waitForConnectionsToClose()

	// Close the server
	log.Println("Closing SMTP server...")
	return r.server.Close()
}

// waitForRetryQueueEmpty waits for the retry queue to be processed
func (r *Relay) waitForRetryQueueEmpty() {
	timeout := time.After(30 * time.Second) // 30 second timeout
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// First, try to process any remaining items quickly
	r.processRemainingQueueItems()

	for {
		select {
		case <-timeout:
			remaining := len(r.retryCh)
			if remaining > 0 {
				log.Printf("Timeout waiting for retry queue to empty, %d items remaining", remaining)
			} else {
				log.Println("Retry queue emptied successfully")
			}
			return
		case <-ticker.C:
			if len(r.retryCh) == 0 {
				log.Println("Retry queue emptied successfully")
				return
			}
		}
	}
}

// waitForConnectionsToClose waits for active connections to close with timeout
func (r *Relay) waitForConnectionsToClose() {
	timeout := time.After(10 * time.Second) // 10 second timeout for connections
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			active := atomic.LoadInt32(&r.activeConnections)
			if active > 0 {
				log.Printf("Timeout waiting for connections to close, %d connections still active", active)
			}
			return
		case <-ticker.C:
			active := atomic.LoadInt32(&r.activeConnections)
			if active == 0 {
				log.Println("All connections closed successfully")
				return
			}
		}
	}
}

// retryWorker processes failed messages for retry
func (r *Relay) retryWorker() {
	ticker := time.NewTicker(30 * time.Second)     // Check for retries every 30 seconds
	saveTicker := time.NewTicker(5 * time.Minute)  // Save queue every 5 minutes
	cleanupTicker := time.NewTicker(1 * time.Hour) // Cleanup every hour
	defer ticker.Stop()
	defer saveTicker.Stop()
	defer cleanupTicker.Stop()

	for {
		select {
		case <-r.stopCh:
			log.Println("Retry worker shutting down, processing remaining queue items...")
			// Process remaining items in queue before shutting down
			r.processRemainingQueueItems()
			// Save final state of retry queue
			if err := r.saveRetryQueue(); err != nil {
				log.Printf("Error saving retry queue on shutdown: %v", err)
			}
			log.Println("Retry worker shutdown complete")
			return
		case msg := <-r.retryCh:
			r.processRetry(msg)
		case <-ticker.C:
			r.checkForRetries()
		case <-saveTicker.C:
			// Periodically save retry queue
			if err := r.saveRetryQueue(); err != nil {
				log.Printf("Error saving retry queue: %v", err)
			}
		case <-cleanupTicker.C:
			// Periodically cleanup old failed messages
			if err := r.cleanupOldFailedMessages(); err != nil {
				log.Printf("Error cleaning up old failed messages: %v", err)
			}
		}
	}
}

// processRemainingQueueItems processes all remaining items in the retry queue
func (r *Relay) processRemainingQueueItems() {
	processed := 0
	for {
		select {
		case msg := <-r.retryCh:
			log.Printf("Processing remaining message %s during shutdown", msg.ID)
			r.processRetry(msg)
			processed++
		default:
			log.Printf("Processed %d remaining messages during shutdown", processed)
			return
		}
	}
}

// processRetry attempts to retry a failed message
func (r *Relay) processRetry(msg *storage.Message) {
	if !r.config.Retry.Enabled {
		return
	}

	// Update activity timestamp to prevent false deadlock detection
	atomic.StoreInt64(&r.lastActivity, time.Now().Unix())

	// Check if we should retry forever or respect max attempts
	maxAttempts := r.config.Retry.MaxAttempts
	if r.config.Retry.RetryForever {
		maxAttempts = -1 // -1 means no limit
	}

	if maxAttempts > 0 && msg.RetryAttempt >= maxAttempts {
		log.Printf("Message %s has exceeded max retry attempts (%d), marking as permanently failed", msg.ID, maxAttempts)
		msg.Status = "failed"
		msg.Error = fmt.Sprintf("Exceeded max retry attempts (%d)", maxAttempts)
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

	log.Printf("Retrying message %s (attempt %d)", msg.ID, msg.RetryAttempt+1)
	if maxAttempts > 0 {
		log.Printf(" (max attempts: %d)", maxAttempts)
	}

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

		// Add retry attempt to history (with limit)
		retryAttempt := storage.RetryAttempt{
			Attempt:   msg.RetryAttempt,
			Timestamp: time.Now(),
			Error:     msg.Error,
		}
		msg.RetryHistory = append(msg.RetryHistory, retryAttempt)

		// Limit retry history size
		if r.config.Retry.MaxRetryHistory > 0 && len(msg.RetryHistory) > r.config.Retry.MaxRetryHistory {
			msg.RetryHistory = msg.RetryHistory[len(msg.RetryHistory)-r.config.Retry.MaxRetryHistory:]
		}

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

	// Send email using proper TLS handling
	done := make(chan error, 1)
	go func() {
		done <- r.sendMailWithTLS(addr, auth, msg.From, msg.To, msg.Body)
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

// sendMailWithTLS sends an email with proper TLS handling based on configuration
func (r *Relay) sendMailWithTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	var conn *smtp.Client
	var err error

	// Create TLS config if needed
	var tlsConfig *tls.Config
	if r.config.Outgoing.TLS.Enabled {
		tlsConfig = &tls.Config{
			ServerName:         r.config.Outgoing.Host,
			InsecureSkipVerify: r.config.Outgoing.TLS.SkipVerify,
		}
	}

	// Connect based on TLS mode
	switch r.config.Outgoing.TLS.Mode {
	case "ssl":
		// SSL/TLS connection (port 465)
		if !r.config.Outgoing.TLS.Enabled {
			return fmt.Errorf("TLS must be enabled for SSL mode")
		}
		// For SSL mode, we need to establish a TLS connection first
		tlsConn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("failed to establish TLS connection to %s: %w", addr, err)
		}
		conn, err = smtp.NewClient(tlsConn, r.config.Outgoing.Host)
		if err != nil {
			tlsConn.Close()
			return fmt.Errorf("failed to create SMTP client: %w", err)
		}

	case "starttls":
		// STARTTLS connection (port 587)
		conn, err = smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("failed to connect to %s: %w", addr, err)
		}
		if r.config.Outgoing.TLS.Enabled {
			if err = conn.StartTLS(tlsConfig); err != nil {
				conn.Close()
				return fmt.Errorf("failed to start TLS: %w", err)
			}
		}

	case "none":
		// Plain connection (port 25)
		conn, err = smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("failed to connect to %s: %w", addr, err)
		}

	default:
		return fmt.Errorf("unsupported TLS mode: %s", r.config.Outgoing.TLS.Mode)
	}

	defer conn.Close()

	// Authenticate if required
	if auth != nil {
		if err := conn.Auth(auth); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}
	}

	// Send the email
	if err := conn.Mail(from); err != nil {
		return fmt.Errorf("failed to set sender: %w", err)
	}

	for _, recipient := range to {
		if err := conn.Rcpt(recipient); err != nil {
			return fmt.Errorf("failed to set recipient %s: %w", recipient, err)
		}
	}

	w, err := conn.Data()
	if err != nil {
		return fmt.Errorf("failed to start data transfer: %w", err)
	}

	_, err = w.Write(msg)
	if err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	err = w.Close()
	if err != nil {
		return fmt.Errorf("failed to close data transfer: %w", err)
	}

	return nil
}

// Backend implements the SMTP backend
type Backend struct {
	config  *config.Config
	storage storage.Storage
	relay   *Relay
}

// NewSession creates a new SMTP session
func (b *Backend) NewSession(conn *gosmtp.Conn) (gosmtp.Session, error) {
	// Track active connection
	atomic.AddInt32(&b.relay.activeConnections, 1)

	return &Session{
		backend: b,
		conn:    conn,
	}, nil
}

// Session represents an SMTP session
type Session struct {
	backend *Backend
	from    string
	to      []string
	conn    *gosmtp.Conn // Added for tracking active connections
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
	// Track connection cleanup
	atomic.AddInt32(&s.backend.relay.activeConnections, -1)
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
	} else {
		// Update activity timestamp to prevent false deadlock detection
		atomic.StoreInt64(&s.backend.relay.lastActivity, time.Now().Unix())
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
	// Check if we should retry forever or respect max attempts
	maxAttempts := s.backend.config.Retry.MaxAttempts
	if s.backend.config.Retry.RetryForever {
		maxAttempts = -1 // -1 means no limit
	}

	if s.backend.config.Retry.Enabled && (maxAttempts < 0 || msg.RetryAttempt < maxAttempts) {
		// Schedule for retry
		msg.Status = "retrying"
		msg.RetryAttempt++
		msg.NextRetry = time.Now().Add(s.backend.config.Retry.InitialDelay)

		// Add retry attempt to history (with limit)
		retryAttempt := storage.RetryAttempt{
			Attempt:   msg.RetryAttempt,
			Timestamp: time.Now(),
			Error:     msg.Error,
		}
		msg.RetryHistory = append(msg.RetryHistory, retryAttempt)

		// Limit retry history size
		if s.backend.config.Retry.MaxRetryHistory > 0 && len(msg.RetryHistory) > s.backend.config.Retry.MaxRetryHistory {
			msg.RetryHistory = msg.RetryHistory[len(msg.RetryHistory)-s.backend.config.Retry.MaxRetryHistory:]
		}

		log.Printf("Message %s scheduled for retry (attempt %d)", msg.ID, msg.RetryAttempt)
		if maxAttempts > 0 {
			log.Printf(" (max attempts: %d)", maxAttempts)
		}

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
