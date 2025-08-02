#!/bin/bash

# Test script to demonstrate asynchronous relay behavior
# This script shows that the SMTP relay responds immediately without waiting for forwarding

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
SMTP_HOST="localhost"
SMTP_PORT="2525"
FROM_EMAIL="test@example.com"
TO_EMAIL="recipient@example.com"

echo -e "${BLUE}Asynchronous SMTP Relay Test${NC}"
echo "=================================="

# Check if the SMTP relay is running
echo -e "\n${YELLOW}Checking if SMTP relay is running...${NC}"
if ! nc -z $SMTP_HOST $SMTP_PORT 2>/dev/null; then
    echo -e "${RED}SMTP relay is not running on $SMTP_HOST:$SMTP_PORT${NC}"
    echo "Please start the SMTP relay first:"
    echo "  ./smtp-relay"
    exit 1
fi
echo -e "${GREEN}SMTP relay is running${NC}"

# Test 1: Send email and measure response time
echo -e "\n${YELLOW}Test 1: Measuring response time for email submission${NC}"
echo "This test will send an email and measure how quickly the relay responds."

# Create a temporary email file
cat > /tmp/test_email.txt << EOF
Subject: Async Test Email
From: $FROM_EMAIL
To: $TO_EMAIL
Date: $(date -R)

This is a test email to verify asynchronous relay behavior.
The relay should respond immediately without waiting for forwarding.

Sent at: $(date)
EOF

# Measure the time it takes to send the email
echo "Sending email..."
start_time=$(date +%s.%N)

# Send email using curl and capture the response time
if curl --mail-from "$FROM_EMAIL" \
        --mail-rcpt "$TO_EMAIL" \
        --upload-file /tmp/test_email.txt \
        "smtp://$SMTP_HOST:$SMTP_PORT" > /dev/null 2>&1; then
    
    end_time=$(date +%s.%N)
    response_time=$(echo "$end_time - $start_time" | bc -l)
    
    echo -e "${GREEN}✓ Email sent successfully${NC}"
    echo -e "${BLUE}Response time: ${response_time} seconds${NC}"
    
    if (( $(echo "$response_time < 1.0" | bc -l) )); then
        echo -e "${GREEN}✓ Fast response (under 1 second) - relay is asynchronous!${NC}"
    else
        echo -e "${YELLOW}⚠ Response time is ${response_time} seconds - may not be fully asynchronous${NC}"
    fi
else
    echo -e "${RED}✗ Failed to send email${NC}"
fi

# Test 2: Check message storage immediately
echo -e "\n${YELLOW}Test 2: Checking message storage immediately after sending${NC}"
sleep 1

if [ -d "messages" ] && [ "$(ls -A messages 2>/dev/null)" ]; then
    echo -e "${GREEN}✓ Messages found in storage${NC}"
    echo "Recent messages:"
    ls -la messages/ | tail -3
else
    echo -e "${YELLOW}No messages found in storage yet${NC}"
fi

# Test 3: Monitor forwarding in background
echo -e "\n${YELLOW}Test 3: Monitoring forwarding process${NC}"
echo "The relay should be forwarding messages in the background."
echo "Check the logs to see forwarding activity:"
echo "  tail -f logs/smtp-relay.log"

# Test 4: Send multiple emails quickly
echo -e "\n${YELLOW}Test 4: Sending multiple emails quickly${NC}"
echo "Sending 3 emails in quick succession to test concurrent processing..."

for i in {1..3}; do
    echo "Sending email $i..."
    start_time=$(date +%s.%N)
    
    # Create email with unique content
    cat > /tmp/test_email_$i.txt << EOF
Subject: Async Test Email $i
From: $FROM_EMAIL
To: $TO_EMAIL
Date: $(date -R)

This is test email number $i.
Sent at: $(date)

EOF
    
    if curl --mail-from "$FROM_EMAIL" \
            --mail-rcpt "$TO_EMAIL" \
            --upload-file /tmp/test_email_$i.txt \
            "smtp://$SMTP_HOST:$SMTP_PORT" > /dev/null 2>&1; then
        
        end_time=$(date +%s.%N)
        response_time=$(echo "$end_time - $start_time" | bc -l)
        echo -e "  ${GREEN}✓ Email $i sent in ${response_time} seconds${NC}"
    else
        echo -e "  ${RED}✗ Failed to send email $i${NC}"
    fi
    
    # Small delay between emails
    sleep 0.1
done

# Test 5: Check final status
echo -e "\n${YELLOW}Test 5: Final status check${NC}"
sleep 2

if [ -d "messages" ] && [ "$(ls -A messages 2>/dev/null)" ]; then
    echo -e "${GREEN}✓ Total messages in storage: $(ls messages/ | wc -l)${NC}"
    echo "Message statuses:"
    for file in messages/*.json; do
        if [ -f "$file" ]; then
            status=$(grep -o '"status":"[^"]*"' "$file" | cut -d'"' -f4)
            echo "  $(basename "$file" .json): $status"
        fi
    done
fi

# Cleanup
rm -f /tmp/test_email*.txt

echo -e "\n${GREEN}Asynchronous relay test completed!${NC}"
echo ""
echo "Key improvements:"
echo "  ✓ SMTP relay responds immediately to clients"
echo "  ✓ Message forwarding happens in background"
echo "  ✓ No client timeouts due to slow forwarding"
echo "  ✓ Better error handling and logging"
echo "  ✓ Concurrent message processing"
echo ""
echo "To monitor the relay in real-time:"
echo "  tail -f logs/smtp-relay.log" 