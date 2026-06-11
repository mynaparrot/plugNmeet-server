#!/bin/sh

# Example Delete Script
# This script is a placeholder to demonstrate how to handle the delete hook.
# It receives an 'input_path' (which is the stored logical path) and should perform the deletion from the external storage.

set -e

INPUT_JSON=$(cat)
# 'input_path' is the path/URL of the file in remote storage to be deleted.
INPUT_PATH=$(echo "$INPUT_JSON" | jq -r '.input_path')

# --- Your Custom Logic Goes Here ---
#
# For a real S3 deletion, you might do:
#   aws s3 rm "$INPUT_PATH"
#
# For rclone:
#   rclone deletefile "$INPUT_PATH"

echo "Received delete request for $INPUT_PATH" >&2

# --- End of Custom Logic ---

# Output a success message. The server only checks for errors.
jq -n '{"msg": "delete request processed"}'
