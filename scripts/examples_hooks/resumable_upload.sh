#!/bin/sh

# Example Long-Lived Resumable Upload Script
# This script runs in a continuous loop, reading newline-delimited JSON from stdin.
# For each request, it must generate a JSON response to stdout.
# This script simulates a storage backend (like S3) using the local filesystem.

# Exit immediately if a command exits with a non-zero status.
set -e

# Add a log function for easier debugging. Output is sent to stderr.
log() {
  echo "$1" >&2
}

# The base directory for our simulated remote storage.
# IMPORTANT: This must be writable by the server process.
SIMULATED_STORAGE_BASE="/tmp/plugnmeet_hook_storage"
log "Starting long-lived resumable upload script. Storage base: $SIMULATED_STORAGE_BASE"

while read -r line; do
  log "Received request: $line"

  # The 'data' field contains the original JSON payload from the server.
  DATA=$(echo "$line" | jq -r '.data')
  TYPE=$(echo "$DATA" | jq -r '.type')
  ROOM_SID=$(echo "$DATA" | jq -r '.room_sid')
  IDENTIFIER=$(echo "$DATA" | jq -r '.resumable_identifier')
  CHUNK_NUMBER=$(echo "$DATA" | jq -r '.resumable_chunk_number')
  INPUT_PATH=$(echo "$DATA" | jq -r '.input_path')
  FILENAME=$(echo "$DATA" | jq -r '.resumable_filename')

  # Directory for the current upload, organized by room and upload identifier
  UPLOAD_DIR="$SIMULATED_STORAGE_BASE/$ROOM_SID/$IDENTIFIER"
  mkdir -p "$UPLOAD_DIR"

  # --- Main Logic ---
  case "$TYPE" in
    "part-check")
      CHUNK_FILE="$UPLOAD_DIR/part.$CHUNK_NUMBER"
      if [ -f "$CHUNK_FILE" ]; then
        # The chunk already exists in our simulated storage
        jq -n --arg type "part_exists" \
              '{output_response_type: $type}'
      else
        # The chunk does not exist
        jq -n --arg type "part_not_exists" \
              '{output_response_type: $type}'
      fi
      ;;

    "part-upload")
      CHUNK_FILE="$UPLOAD_DIR/part.$CHUNK_NUMBER"
      # Simulate uploading by copying the chunk from the server's temp location
      # to our simulated remote storage.
      cp "$INPUT_PATH" "$CHUNK_FILE"

      # Respond that the part was "uploaded"
      jq -n --arg type "part_uploaded" \
            '{output_response_type: $type}'
      ;;

    "merge")
      FINAL_FILE_DIR="$SIMULATED_STORAGE_BASE/$ROOM_SID/merged"
      FINAL_FILE_PATH="$FINAL_FILE_DIR/$FILENAME"
      mkdir -p "$FINAL_FILE_DIR"

      # Simulate merging by concatenating all the parts in order.
      # 'ls -v' sorts numerically (part.1, part.2, ... part.10).
      for part in $(ls -v "$UPLOAD_DIR"/part.*); do
        cat "$part" >> "$FINAL_FILE_PATH"
      done

      # Clean up the individual parts after merging
      rm -rf "$UPLOAD_DIR"

      # In a real script, you would get the mime type and extension properly.
      # Here we just hardcode them for the example.
      MIME_TYPE="application/octet-stream"
      EXTENSION="bin"
      if [ -n "$FILENAME" ] && echo "$FILENAME" | grep -q '\.'; then
          EXTENSION=$(echo "$FILENAME" | rev | cut -d'.' -f1 | rev)
          case "$EXTENSION" in
              "zip") MIME_TYPE="application/zip";;
              "pdf") MIME_TYPE="application/pdf";;
              "jpg") MIME_TYPE="image/jpeg";;
              "png") MIME_TYPE="image/png";;
          esac
      fi

      # Respond with the final path and metadata
      jq -n --arg type "merge_success" \
            --arg path "$FINAL_FILE_PATH" \
            --arg mime "$MIME_TYPE" \
            --arg ext "$EXTENSION" \
            '{output_response_type: $type, output_path: $path, file_mime_type: $mime, file_extension: $ext}'
      ;;

    *)
      # Handle unknown type
      log "Error: Unknown hook type: $TYPE"
      jq -n --arg err "Unknown hook type: $TYPE" \
            '{error: $err}'
      ;;
  esac
done
