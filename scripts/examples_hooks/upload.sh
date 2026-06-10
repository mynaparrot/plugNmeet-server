#!/bin/sh

# Example Upload Script
# This script is a placeholder to demonstrate how to handle the upload hook.
# In a real-world scenario, you would use a tool like `rclone` or a cloud provider's CLI
# to upload the file and then output the new logical path.

# Exit immediately if a command exits with a non-zero status.
set -e

# Read the JSON input from stdin
INPUT_JSON=$(cat)

# Use jq to extract the source file path and service type.
# jq is a lightweight and powerful command-line JSON processor.
SOURCE_FILE=$(echo "$INPUT_JSON" | jq -r '.source_file_path')
SERVICE_TYPE=$(echo "$INPUT_JSON" | jq -r '.service_type')

# --- Your Custom Logic Goes Here ---
#
# Example: Upload to a pseudo "cloud" directory and create a new logical path.
# This simulates uploading the file and getting a new identifier.
#
# For a real S3 upload, you might do something like:
#   aws s3 cp "$SOURCE_FILE" "s3://my-bucket/$SERVICE_TYPE/$(basename "$SOURCE_FILE")"
#   LOGICAL_PATH="s3://my-bucket/$SERVICE_TYPE/$(basename "$SOURCE_FILE")"
#
# For rclone:
#   rclone copyto "$SOURCE_FILE" "my-remote:$SERVICE_TYPE/$(basename "$SOURCE_FILE")"
#   LOGICAL_PATH="my-remote:$SERVICE_TYPE/$(basename "$SOURCE_FILE")"

# For this example, we'll just log it and construct a fake "logical" path.
# We are not actually moving the file, just demonstrating the data flow.
echo "Received upload request for $SOURCE_FILE (service: $SERVICE_TYPE)" >&2

# Construct a new logical path.
# In this example, we'll pretend we uploaded it to a provider called "mycloud".
LOGICAL_PATH="mycloud://$SERVICE_TYPE/$(basename "$SOURCE_FILE")"

# --- End of Custom Logic ---

# The final step is to output a JSON object containing the new logical_path.
# This will be read by the plugNmeet server and stored in the database.
jq -n --arg logical_path "$LOGICAL_PATH" '{"logical_path": $logical_path}'
