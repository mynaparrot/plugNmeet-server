#!/bin/sh

# Example Long-Lived Delete Script
# This script runs in a continuous loop, reading newline-delimited JSON from stdin.
# For each request, it should perform the deletion from the external storage and generate a JSON response to stdout.

# Exit immediately if a command exits with a non-zero status.
set -e

# Add a log function for easier debugging. Output is sent to stderr.
log() {
  echo "$1" >&2
}

log "Starting long-lived delete script..."

while read -r line; do
  log "Received request: $line"

  # The 'data' field contains the original JSON payload.
  # We extract the 'input_path' (the logical path of the file to delete).
  INPUT_PATH=$(echo "$line" | jq -r '.data.input_path')

  if [ -z "$INPUT_PATH" ] || [ "$INPUT_PATH" = "null" ]; then
    log "Error: input_path is missing from request."
    jq -n '{"error": "input_path is missing"}'
    continue
  fi

  # --- Your Custom Logic Goes Here ---
  #
  # For a real S3 deletion, you might do:
  #   aws s3 rm "$INPUT_PATH"
  #
  # For rclone:
  #   rclone deletefile "$INPUT_PATH"

  log "Received delete request for $INPUT_PATH"

  # --- End of Custom Custom Logic ---

  # Output a success message. The server only checks for errors.
  jq -n '{"msg": "delete request processed"}'

done