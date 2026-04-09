#!/bin/sh
set -e

# Start Go bot in background (WhatsApp + ingest on port 8080)
nagger &
BOT_PID=$!

# Start Next.js dashboard (port 3000)
cd /app/dashboard
DB_PATH=/data/tasks.db PORT=3000 HOSTNAME=0.0.0.0 node server.js &
DASH_PID=$!

echo "Bot PID=$BOT_PID, Dashboard PID=$DASH_PID"

# If either process exits, shut down the other
wait -n $BOT_PID $DASH_PID
EXIT_CODE=$?
echo "Process exited with code $EXIT_CODE, shutting down..."
kill $BOT_PID $DASH_PID 2>/dev/null
exit $EXIT_CODE
