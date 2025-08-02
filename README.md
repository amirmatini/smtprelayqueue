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
- **Robust Error Handling**: Comprehensive error handling with panic recovery

## Installation

### Prerequisites

- Go 1.21 or later (for building from source)
- Docker (for containerized deployment)

### Quick Installation

#### Using Docker

```bash
# Pull and run the container
docker run -d \
  --name smtp-relay \
  -p 2525:2525 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  -v $(pwd)/messages:/app/messages \
  -v $(pwd)/logs:/app/logs \
  yourusername/smtp-relay:latest
```

### Building from Source

```bash
# Clone the repository
git clone <repository-url>
cd SMTPRelay

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

### Supported Architectures

The SMTP Relay supports multiple architectures:
- **Linux**: AMD64, ARM64, ARMv7
- **macOS**: AMD64, ARM64 (Apple Silicon)
- **Windows**: AMD64, ARM64

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