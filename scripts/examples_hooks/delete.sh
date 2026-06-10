#!/bin/sh

# Example Delete Script
# This script is a placeholder to demonstrate how to handle the delete hook.
# It receives a 'logical_path' and should perform the deletion from the external storage.

set -e

INPUT_JSON=$(cat)
LOGICAL_PATH=$(echo "$INPUT_JSON" | jq -r '.logical_path')

# --- Your Custom Logic Goes Here ---
#
# For a real S3 deletion, you might do:
#   aws s3 rm "$LOGICAL_PATH"
#
# For rclone:
#   rclone deletefile "$LOGICAL_PATH"

echo "Received delete request for $LOGICAL_PATH" >&2

# --- End of Custom Logic ---

# Output a success message. The server only checks for errors.
jq -n '{"msg": "delete request processed"}'
