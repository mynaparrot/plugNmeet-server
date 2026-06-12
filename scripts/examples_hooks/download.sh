#!/bin/sh

# Example Long-Lived Download Script
# This script runs in a continuous loop, reading newline-delimited JSON from stdin.
# For each request, it must generate a JSON response to stdout.

# Exit immediately if a command exits with a non-zero status.
set -e

# Add a log function for easier debugging. Output is sent to stderr.
log() {
  echo "$1" >&2
}

log "Starting long-lived download script..."

while read -r line; do
  log "Received request: $line"

  # The 'data' field contains the original JSON payload.
  # We extract the 'input_path' from it.
  INPUT_PATH=$(echo "$line" | jq -r '.data.input_path')

  if [ -z "$INPUT_PATH" ] || [ "$INPUT_PATH" = "null" ]; then
    log "Error: input_path is missing from request."
    jq -n '{"error": "input_path is missing"}'
    continue
  fi

  # --- Your Custom Logic Goes Here ---
  #
  # The script needs to check the scheme of the input_path (e.g., "mycloud://", "s3://")
  # and then generate the appropriate response.

  ACTION=""
  REDIRECT_URL=""
  OUTPUT_PATH=""

  # Example logic for a pseudo "mycloud" provider
  if echo "$INPUT_PATH" | grep -q "mycloud://"; then
    # In a real scenario, you would call your cloud provider's API or CLI
    # to generate a temporary, pre-signed URL.
    #
    # For S3:
    #   REDIRECT_URL=$(aws s3 presign "$INPUT_PATH" --expires-in 300)
    #
    log "Generating a redirect for $INPUT_PATH"
    ACTION="redirect"
    REDIRECT_URL="https://my-fake-cloud.com/download?path=$(echo "$INPUT_PATH" | sed 's|mycloud://||')"
  else
    # If the path is not a cloud path, you can tell the server to serve a local file.
    log "Treating $INPUT_PATH as a local file."
    ACTION="serve_local"
    OUTPUT_PATH="$INPUT_PATH" # Assuming the path is already valid on the server's filesystem
  fi
  # --- End of Custom Custom Logic ---

  # The final step is to output a JSON object to stdout.
  # This tells the plugNmeet server how to deliver the file to the user.
  jq -n \
    --arg action "$ACTION" \
    --arg redirect_url "$REDIRECT_URL" \
    --arg output_path "$OUTPUT_PATH" \
    '{"action": $action, "redirect_url": $redirect_url, "output_path": $output_path}'

done