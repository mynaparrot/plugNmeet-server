#!/bin/sh

# Example Long-Lived Room End Script
# This script runs in a continuous loop, reading newline-delimited JSON from stdin.
# It's designed to clean up resources associated with a room after it has ended.

# Exit immediately if a command exits with a non-zero status.
set -e

# Add a log function for easier debugging. Output is sent to stderr.
log() {
  echo "$1" >&2
}

# The base directory for our simulated remote storage.
# This must match the one used in the resumable_upload.sh script.
SIMULATED_STORAGE_BASE="/tmp/plugnmeet_hook_storage"
log "Starting long-lived room end script. Monitoring storage base: $SIMULATED_STORAGE_BASE"

while read -r line; do
  log "Received request: $line"

  # The 'data' field contains the original JSON payload from the server.
  DATA=$(echo "$line" | jq -r '.data')
  ROOM_SID=$(echo "$DATA" | jq -r '.room_sid')

  if [ -z "$ROOM_SID" ] || [ "$ROOM_SID" = "null" ]; then
    log "Error: room_sid is missing from request."
    jq -n '{"error": "room_sid is missing"}'
    continue
  fi

  # --- Your Custom Cleanup Logic Goes Here ---
  #
  # This script should clean up any temporary or abandoned resources for the given room.
  # For our example, this means deleting any directories related to the room_sid
  # in our simulated storage. This handles both merged files and any lingering
  # chunk directories from incomplete uploads.

  ROOM_STORAGE_PATH="$SIMULATED_STORAGE_BASE/$ROOM_SID"

  if [ -d "$ROOM_STORAGE_PATH" ]; then
    log "Found storage for room $ROOM_SID. Deleting path: $ROOM_STORAGE_PATH"
    rm -rf "$ROOM_STORAGE_PATH"
    MSG="Cleaned up storage for room $ROOM_SID."
  else
    log "No storage found for room $ROOM_SID. Nothing to do."
    MSG="No storage to clean up for room $ROOM_SID."
  fi

  # --- End of Custom Cleanup Logic ---

  # Respond with a success message.
  jq -n --arg msg "$MSG" '{"msg": $msg}'

done
