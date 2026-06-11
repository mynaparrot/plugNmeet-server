#!/bin/sh

# Example Download Script
# This script is a placeholder to demonstrate how to handle the download hook.
# It receives an 'input_path' (which is the stored logical path) and must decide how the user should access the file.

# Exit immediately if a command exits with a non-zero status.
set -e

# Read the JSON input from stdin
INPUT_JSON=$(cat)
# 'input_path' is the path/URL of the file in remote storage to be downloaded.
INPUT_PATH=$(echo "$INPUT_JSON" | jq -r '.input_path')

# --- Your Custom Logic Goes Here ---
#
# The script needs to check the scheme of the input_path (e.g., "mycloud://", "s3://")
# and then generate the appropriate response.

ACTION=""
REDIRECT_URL=""
OUTPUT_PATH="" # Renamed from LOCAL_PATH

# Example logic for our pseudo "mycloud" provider
if echo "$INPUT_PATH" | grep -q "mycloud://"; then
  #
  # In a real scenario, you would call your cloud provider's API or CLI
  # to generate a temporary, pre-signed URL.
  #
  # For S3:
  #   REDIRECT_URL=$(aws s3 presign "$INPUT_PATH" --expires-in 300)
  #
  # For this example, we'll just log it and create a fake redirect URL.
  echo "Generating a redirect for $INPUT_PATH" >&2
  ACTION="redirect"
  REDIRECT_URL="https://my-fake-cloud.com/download?path=$(echo "$INPUT_PATH" | sed 's|mycloud://||')"

else
  #
  # If the path is not a cloud path, or if you want to handle it locally,
  # you can tell the server to serve a local file.
  # This is useful if your script needs to download the file from a secure location
  # to a temporary local path that the server can access.
  #
  echo "Treating $INPUT_PATH as a local file." >&2
  ACTION="serve_local"
  OUTPUT_PATH="$INPUT_PATH" # Assuming the path is already valid on the server's filesystem
fi

# --- End of Custom Logic ---

# The final step is to output a JSON object with the chosen action and the corresponding URL or path.
# This tells the plugNmeet server how to deliver the file to the user.
jq -n \
  --arg action "$ACTION" \
  --arg redirect_url "$REDIRECT_URL" \
  --arg output_path "$OUTPUT_PATH" \
  '{"action": $action, "redirect_url": $redirect_url, "output_path": $output_path}'
