#!/bin/sh

# Example Long-Lived Upload Script
# This script runs in a continuous loop, reading newline-delimited JSON from stdin.
# For each request, it must generate a JSON response to stdout.

# Exit immediately if a command exits with a non-zero status.
set -e

# Add a log function for easier debugging. Output is sent to stderr.
log() {
  echo "$1" >&2
}

log "Starting long-lived upload script..."

while read -r line; do
  log "Received request: $line"

  # The 'data' field contains the original JSON payload.
  # We extract the 'input_path' (local file to upload) and 'service_type'.
  INPUT_PATH=$(echo "$line" | jq -r '.data.input_path')
  SERVICE_TYPE=$(echo "$line" | jq -r '.data.service_type')

  if [ -z "$INPUT_PATH" ] || [ "$INPUT_PATH" = "null" ]; then
    log "Error: input_path is missing from request."
    jq -n '{"error": "input_path is missing"}'
    continue
  fi
  if [ -z "$SERVICE_TYPE" ] || [ "$SERVICE_TYPE" = "null" ]; then
    log "Error: service_type is missing from request."
    jq -n '{"error": "service_type is missing"}'
    continue
  fi

  # --- Your Custom Logic Goes Here ---
  #
  # Example: Upload to a pseudo "cloud" directory and create a new logical path.
  # This simulates uploading the file and getting a new identifier.
  #
  # For a real S3 upload, you might do something like:
  #   aws s3 cp "$INPUT_PATH" "s3://my-bucket/$SERVICE_TYPE/$(basename "$INPUT_PATH")"
  #   OUTPUT_PATH="s3://my-bucket/$SERVICE_TYPE/$(basename "$INPUT_PATH")"
  #
  # For rclone:
  #   rclone copyto "$INPUT_PATH" "my-remote:$SERVICE_TYPE/$(basename "$INPUT_PATH")"
  #   OUTPUT_PATH="my-remote:$SERVICE_TYPE/$(basename "$INPUT_PATH")"

  # For this example, we'll just log it and construct a fake "output" path.
  # We are not actually moving the file, just demonstrating the data flow.
  log "Received upload request for $INPUT_PATH (service: $SERVICE_TYPE)"

  # Construct a new output path.
  # In this example, we'll pretend we uploaded it to a provider called "mycloud".
  OUTPUT_PATH="mycloud://$SERVICE_TYPE/$(basename "$INPUT_PATH")"

  # --- End of Custom Custom Logic ---

  # The final step is to output a JSON object containing the new output_path.
  # This will be read by the plugNmeet server and stored in the database.
  jq -n --arg output_path "$OUTPUT_PATH" '{"output_path": $output_path}'

done