# SMTP Relay

A Go-based SMTP relay server that receives emails, stores them with all headers and information, and forwards them to another SMTP server. Supports various authentication methods and TLS configurations.

## Features

- **SMTP Server**: Acts as a real SMTP server to receive emails
- **Asynchronous Processing**: Responds immediately to clients, processes forwarding in background
- **Message Storage**: Stores all emails with headers and metadata
- **SMTP Forwarding**: Forwards emails to target SMTP servers with timeout handling
- **Authentication Support**: Supports PLAIN, LOGIN, and CRAM-MD5 authentication
- **TLS/SSL Support**: Supports STARTTLS, SSL, and no encryption
- **Flexible Storage**: File-based or in-memory storage
- **Configuration**: YAML-based configuration
- **Rate Limiting**: Built-in rate limiting support
- **Retry Mechanism**: Configurable retry logic for failed emails with exponential backoff
- **Robust Error Handling**: Comprehensive error handling with panic recovery

## Installation

### Prerequisites

- Go 1.21 or later (for building from source)

### Quick Installation

Download the latest binary for your platform from the [GitHub releases](https://github.com/amirmatini/smtprelayqueue/releases) page.

### Building from Source

```bash
# Clone the repository
git clone https://github.com/amirmatini/smtprelayqueue.git
cd smtprelayqueue

# Build the application
go build -o smtp-relay cmd/smtp-relay/main.go
```

### Systemd Service

For Linux deployment, you can use the provided systemd service file:

```bash
# Copy the service file
sudo cp systemd/smtp-relay.service /etc/systemd/system/

# Create the service user
sudo useradd -r -s /bin/false smtp-relay

### Supported Platforms

The SMTP Relay supports the following platforms:
- **Linux AMD64**: x86_64 architecture
- **Linux ARM64**: ARM64 architecture (including Raspberry Pi 4)

Pre-built binaries are available for these platforms in the [GitHub releases](https://github.com/amirmatini/smtprelayqueue/releases).
# Create the application directory
sudo mkdir -p /opt/smtp-relay
sudo cp smtp-relay /opt/smtp-relay/
sudo cp config.yaml /opt/smtp-relay/

# Set permissions
sudo chown -R smtp-relay:smtp-relay /opt/smtp-relay
sudo chmod +x /opt/smtp-relay/smtp-relay

# Enable and start the service
sudo systemctl daemon-reload
sudo systemctl enable smtp-relay
sudo systemctl start smtp-relay
```



## Configuration

The application uses a YAML configuration file. See `config.yaml` for a complete example.

### Configuration Options

#### Incoming SMTP Server
- `host`: Host to bind to (default: "0.0.0.0")
- `port`: Port to listen on (default: 2525)
- `tls.enabled`: Enable TLS for incoming connections
- `tls.cert_file`: Path to TLS certificate file
- `tls.key_file`: Path to TLS private key file
- `auth.enabled`: Enable authentication
- `auth.username`: Username for authentication
- `auth.password`: Password for authentication
- `auth.method`: Authentication method (plain, login, cram-md5)

#### Outgoing SMTP Server
- `host`: Target SMTP server hostname
- `port`: Target SMTP server port
- `tls.enabled`: Enable TLS for outgoing connections
- `tls.mode`: TLS mode (starttls, ssl, none)
- `tls.skip_verify`: Skip TLS certificate verification
- `auth.enabled`: Enable authentication
- `auth.username`: Username for authentication
- `auth.password`: Password for authentication
- `auth.method`: Authentication method (plain, login, cram-md5)

#### Storage
- `type`: Storage type (file, memory)
- `file.path`: Path for file storage
#### Retry Configuration
- `enabled`: Enable retry mechanism for failed emails
- `max_attempts`: Maximum number of retry attempts
- `initial_delay`: Initial delay before first retry (e.g., "5s", "1m")
- `max_delay`: Maximum delay between retries (e.g., "5m", "1h")
- `backoff_multiplier`: Multiplier for exponential backoff (e.g., 2.0)
- `retry_queue_size`: Size of the retry queue
- `file.max_size`: Maximum storage size
- `memory.max_messages`: Maximum number of messages in memory

#### Logging
- `level`: Log level (debug, info, warn, error)
- `format`: Log format (json, text)
- `file`: Log file path

#### Rate Limiting
- `enabled`: Enable rate limiting
- `requests_per_minute`: Requests per minute limit
- `burst`: Burst limit

## Usage

### Basic Usage

```bash
# Run with default configuration
./smtp-relay

# Run with custom configuration file
./smtp-relay -config /path/to/config.yaml
```

### Example Configuration

```yaml
# Incoming SMTP Server
incoming:
  host: "0.0.0.0"
  port: 2525
  tls:
    enabled: false
  auth:
    enabled: false

# Outgoing SMTP Server (Gmail example)
outgoing:
  host: "smtp.gmail.com"
  port: 587
  tls:
    enabled: true
    mode: "starttls"
    skip_verify: false
  auth:
    enabled: true
    username: "your-email@gmail.com"
    password: "your-app-password"
    method: "plain"

# Storage
storage:
# Retry Configuration
retry:
  enabled: true
  max_attempts: 3
  initial_delay: "5s"
  max_delay: "5m"
  backoff_multiplier: 2.0
  retry_queue_size: 100
  type: "file"
  file:
    path: "./messages"
    max_size: "100MB"

# Logging
logging:
  level: "info"
  format: "text"
  file: "./logs/smtp-relay.log"
```

## Asynchronous Processing

The SMTP relay is designed to provide fast response times by processing messages asynchronously:

### How It Works

1. **Immediate Response**: When a client sends an email, the relay immediately:
   - Stores the message locally
   - Responds to the client with success
   - Logs the receipt

2. **Background Forwarding**: The relay then processes forwarding in the background:
   - Updates message status to "forwarding"
   - Attempts to send to the target SMTP server
   - Handles timeouts (30-second limit)
## Retry Mechanism

The SMTP relay includes a configurable retry mechanism for failed emails:

### How Retry Works

1. **Initial Failure**: When an email fails to forward, it is marked as "retrying"
2. **Retry Scheduling**: Failed emails are scheduled for retry with exponential backoff
3. **Retry Processing**: A background worker processes retry attempts
4. **Success or Permanent Failure**: After max attempts, emails are marked as permanently failed

### Retry Configuration

- **Max Attempts**: Configurable number of retry attempts (default: 3)
- **Exponential Backoff**: Delay increases with each retry attempt
- **Queue Management**: Failed emails are queued for retry processing
- **Status Tracking**: Each retry attempt is logged and tracked

### Retry Status Flow

```
received → forwarding → [SUCCESS] → forwarded
                ↓
            [FAILURE] → retrying → [RETRY SUCCESS] → forwarded
                ↓
            [RETRY FAILURE] → retrying → ... → failed (after max attempts)
```
   - Updates final status (forwarded/failed)

### Benefits

- **Fast Client Response**: No waiting for slow target servers
- **No Timeouts**: Clients don't experience connection timeouts
- **Concurrent Processing**: Multiple emails can be processed simultaneously
- **Reliable**: Failed forwarding doesn't affect client experience
- **Observable**: All status changes are logged and stored

### Monitoring

You can monitor the asynchronous processing:

```bash
# Watch real-time logs
tail -f logs/smtp-relay.log

# Check message statuses
ls -la messages/
cat messages/*.json | jq '.status'
```

## Testing

### Using telnet

```bash
# Connect to the SMTP relay
telnet localhost 2525

# Basic SMTP conversation
EHLO localhost
MAIL FROM:<sender@example.com>
RCPT TO:<recipient@example.com>
DATA
Subject: Test Email
From: sender@example.com
To: recipient@example.com

This is a test email.
.
QUIT
```

### Using curl

```bash
# Send email using curl (if no authentication required)
curl --mail-from "sender@example.com" \
     --mail-rcpt "recipient@example.com" \
     --upload-file email.txt \
     smtp://localhost:2525
```

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details. 