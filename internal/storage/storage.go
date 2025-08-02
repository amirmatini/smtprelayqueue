// Copyright (c) 2024 SMTP Relay Contributors
// Licensed under the MIT License

package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"smtp-relay/internal/config"
)

// Message represents a stored email message
type Message struct {
	ID           string            `json:"id"`
	From         string            `json:"from"`
	To           []string          `json:"to"`
	Headers      map[string]string `json:"headers"`
	Body         []byte            `json:"body"`
	Received     time.Time         `json:"received"`
	Forwarded    time.Time         `json:"forwarded,omitempty"`
	Status       string            `json:"status"` // received, forwarding, forwarded, failed, retrying
	Error        string            `json:"error,omitempty"`
	RetryAttempt int               `json:"retry_attempt,omitempty"`
	NextRetry    time.Time         `json:"next_retry,omitempty"`
	RetryHistory []RetryAttempt    `json:"retry_history,omitempty"`
}

// RetryAttempt represents a single retry attempt
type RetryAttempt struct {
	Attempt   int       `json:"attempt"`
	Timestamp time.Time `json:"timestamp"`
	Error     string    `json:"error"`
}

// Storage interface defines the methods for message storage
type Storage interface {
	Store(msg *Message) error
	Get(id string) (*Message, error)
	List(limit int) ([]*Message, error)
	Update(id string, msg *Message) error
	Delete(id string) error
	GetFailedMessages() ([]*Message, error)
	SaveRetryQueue(messages []*Message) error
	LoadRetryQueue() ([]*Message, error)
	Close() error
}

// FileStorage implements file-based storage
type FileStorage struct {
	path    string
	maxSize int64
	mu      sync.RWMutex
}

// MemoryStorage implements in-memory storage
type MemoryStorage struct {
	messages map[string]*Message
	maxCount int
	mu       sync.RWMutex
}

// New creates a new storage instance based on configuration
func New(cfg config.StorageConfig) (Storage, error) {
	switch cfg.Type {
	case "file":
		return NewFileStorage(cfg.File)
	case "memory":
		return NewMemoryStorage(cfg.Memory)
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", cfg.Type)
	}
}

// NewFileStorage creates a new file-based storage
func NewFileStorage(cfg config.FileConfig) (*FileStorage, error) {
	if err := os.MkdirAll(cfg.Path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	return &FileStorage{
		path: cfg.Path,
	}, nil
}

// NewMemoryStorage creates a new memory-based storage
func NewMemoryStorage(cfg config.MemConfig) (*MemoryStorage, error) {
	return &MemoryStorage{
		messages: make(map[string]*Message),
		maxCount: cfg.MaxMessages,
	}, nil
}

// FileStorage methods
func (fs *FileStorage) Store(msg *Message) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	filename := filepath.Join(fs.path, msg.ID+".json")
	data, err := json.MarshalIndent(msg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write message file: %w", err)
	}

	return nil
}

func (fs *FileStorage) Get(id string) (*Message, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	filename := filepath.Join(fs.path, id+".json")
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read message file: %w", err)
	}

	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal message: %w", err)
	}

	return &msg, nil
}

func (fs *FileStorage) List(limit int) ([]*Message, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	files, err := os.ReadDir(fs.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read storage directory: %w", err)
	}

	var messages []*Message
	count := 0

	for _, file := range files {
		if count >= limit && limit > 0 {
			break
		}

		if filepath.Ext(file.Name()) == ".json" {
			id := file.Name()[:len(file.Name())-5] // Remove .json extension
			if msg, err := fs.Get(id); err == nil {
				messages = append(messages, msg)
				count++
			}
		}
	}

	return messages, nil
}

func (fs *FileStorage) Update(id string, msg *Message) error {
	return fs.Store(msg)
}

func (fs *FileStorage) Delete(id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	filename := filepath.Join(fs.path, id+".json")
	return os.Remove(filename)
}

func (fs *FileStorage) Close() error {
	return nil
}

func (fs *FileStorage) GetFailedMessages() ([]*Message, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	files, err := os.ReadDir(fs.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read storage directory: %w", err)
	}

	var failedMessages []*Message

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".json" {
			id := file.Name()[:len(file.Name())-5] // Remove .json extension
			if msg, err := fs.Get(id); err == nil {
				if msg.Status == "failed" || msg.Status == "retrying" {
					failedMessages = append(failedMessages, msg)
				}
			}
		}
	}

	return failedMessages, nil
}

func (fs *FileStorage) SaveRetryQueue(messages []*Message) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Save retry queue to a special file
	filename := filepath.Join(fs.path, "retry_queue.json")

	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal retry queue: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to save retry queue: %w", err)
	}

	return nil
}

func (fs *FileStorage) LoadRetryQueue() ([]*Message, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	filename := filepath.Join(fs.path, "retry_queue.json")

	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			// No retry queue file exists, return empty slice
			return []*Message{}, nil
		}
		return nil, fmt.Errorf("failed to read retry queue: %w", err)
	}

	var messages []*Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, fmt.Errorf("failed to unmarshal retry queue: %w", err)
	}

	return messages, nil
}

// MemoryStorage methods
func (ms *MemoryStorage) Store(msg *Message) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if len(ms.messages) >= ms.maxCount && ms.maxCount > 0 {
		// Remove oldest message if at capacity
		var oldestID string
		var oldestTime time.Time
		for id, message := range ms.messages {
			if oldestID == "" || message.Received.Before(oldestTime) {
				oldestID = id
				oldestTime = message.Received
			}
		}
		if oldestID != "" {
			delete(ms.messages, oldestID)
		}
	}

	ms.messages[msg.ID] = msg
	return nil
}

func (ms *MemoryStorage) Get(id string) (*Message, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	msg, exists := ms.messages[id]
	if !exists {
		return nil, fmt.Errorf("message not found: %s", id)
	}

	return msg, nil
}

func (ms *MemoryStorage) List(limit int) ([]*Message, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	var messages []*Message
	count := 0

	for _, msg := range ms.messages {
		if count >= limit && limit > 0 {
			break
		}
		messages = append(messages, msg)
		count++
	}

	return messages, nil
}

func (ms *MemoryStorage) Update(id string, msg *Message) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if _, exists := ms.messages[id]; !exists {
		return fmt.Errorf("message not found: %s", id)
	}

	ms.messages[id] = msg
	return nil
}

func (ms *MemoryStorage) Delete(id string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if _, exists := ms.messages[id]; !exists {
		return fmt.Errorf("message not found: %s", id)
	}

	delete(ms.messages, id)
	return nil
}

func (ms *MemoryStorage) Close() error {
	return nil
}

func (ms *MemoryStorage) GetFailedMessages() ([]*Message, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	var failedMessages []*Message

	for _, msg := range ms.messages {
		if msg.Status == "failed" || msg.Status == "retrying" {
			failedMessages = append(failedMessages, msg)
		}
	}

	return failedMessages, nil
}

func (ms *MemoryStorage) SaveRetryQueue(messages []*Message) error {
	// For memory storage, we don't need to persist the queue
	// as it will be lost on restart anyway
	return nil
}

func (ms *MemoryStorage) LoadRetryQueue() ([]*Message, error) {
	// For memory storage, we can't load a queue from restart
	// as it's not persisted, so return empty slice
	return []*Message{}, nil
}
