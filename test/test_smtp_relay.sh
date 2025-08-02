#!/bin/bash

# SMTP Relay Test Script
# This script demonstrates how to test the SMTP relay

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
SMTP_HOST="localhost"
SMTP_PORT="2525"
FROM_EMAIL="test@example.com"
TO_EMAIL="recipient@example.com"
TEST_FILE="test_email.txt"

echo -e "${YELLOW}SMTP Relay Test Script${NC}"
echo "=========================="

# Check if the SMTP relay is running
echo -e "\n${YELLOW}Checking if SMTP relay is running...${NC}"
if ! nc -z $SMTP_HOST $SMTP_PORT 2>/dev/null; then
    echo -e "${RED}SMTP relay is not running on $SMTP_HOST:$SMTP_PORT${NC}"
    echo "Please start the SMTP relay first:"
    echo "  ./smtp-relay"
    exit 1
fi
echo -e "${GREEN}SMTP relay is running${NC}"

# Test 1: Basic SMTP connection
echo -e "\n${YELLOW}Test 1: Basic SMTP connection${NC}"
if echo "QUIT" | nc $SMTP_HOST $SMTP_PORT | grep -q "220"; then
    echo -e "${GREEN}✓ SMTP connection successful${NC}"
else
    echo -e "${RED}✗ SMTP connection failed${NC}"
    exit 1
fi

# Test 2: Send email using curl
echo -e "\n${YELLOW}Test 2: Sending email using curl${NC}"
if command -v curl >/dev/null 2>&1; then
    if curl --mail-from "$FROM_EMAIL" \
            --mail-rcpt "$TO_EMAIL" \
            --upload-file "$TEST_FILE" \
            "smtp://$SMTP_HOST:$SMTP_PORT" 2>/dev/null; then
        echo -e "${GREEN}✓ Email sent successfully using curl${NC}"
    else
        echo -e "${RED}✗ Failed to send email using curl${NC}"
    fi
else
    echo -e "${YELLOW}curl not available, skipping curl test${NC}"
fi

# Test 3: Manual SMTP conversation
echo -e "\n${YELLOW}Test 3: Manual SMTP conversation${NC}"
echo "This test will open a telnet session to the SMTP relay."
echo "You can manually test the SMTP protocol."
echo ""
echo "Commands to try:"
echo "  EHLO localhost"
echo "  MAIL FROM:<$FROM_EMAIL>"
echo "  RCPT TO:<$TO_EMAIL>"
echo "  DATA"
echo "  Subject: Manual Test"
echo "  From: $FROM_EMAIL"
echo "  To: $TO_EMAIL"
echo ""
echo "  This is a manual test email."
echo "  ."
echo "  QUIT"
echo ""
read -p "Press Enter to start telnet session (or Ctrl+C to skip)..."
telnet $SMTP_HOST $SMTP_PORT

# Test 4: Check message storage
echo -e "\n${YELLOW}Test 4: Checking message storage${NC}"
if [ -d "messages" ] && [ "$(ls -A messages 2>/dev/null)" ]; then
    echo -e "${GREEN}✓ Messages found in storage${NC}"
    echo "Stored messages:"
    ls -la messages/
else
    echo -e "${YELLOW}No messages found in storage (this is normal if no emails were sent)${NC}"
fi

# Test 5: Check logs
echo -e "\n${YELLOW}Test 5: Checking logs${NC}"
if [ -f "logs/smtp-relay.log" ]; then
    echo -e "${GREEN}✓ Log file found${NC}"
    echo "Recent log entries:"
    tail -10 logs/smtp-relay.log
else
    echo -e "${YELLOW}No log file found${NC}"
fi

echo -e "\n${GREEN}Test completed!${NC}"
echo ""
echo "To view stored messages in detail:"
echo "  cat messages/*.json | jq '.'"
echo ""
echo "To monitor logs in real-time:"
echo "  tail -f logs/smtp-relay.log" 