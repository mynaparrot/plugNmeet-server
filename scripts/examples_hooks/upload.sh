#!/bin/sh

# Example Upload Script
# This script is a placeholder to demonstrate how to handle the upload hook.
# In a real-world scenario, you would use a tool like `rclone` or a cloud provider's CLI
# to upload the file and then output the new logical path.

# Exit immediately if a command exits with a non-zero status.
set -e

# Read the JSON input from stdin
INPUT_JSON=$(cat)

# Use jq to extract the input path and service type.
# 'input_path' is the path to the file on the local system to be uploaded.
INPUT_PATH=$(echo "$INPUT_JSON" | jq -r '.input_path')
SERVICE_TYPE=$(echo "$INPUT_JSON" | jq -r '.service_type')

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
echo "Received upload request for $INPUT_PATH (service: $SERVICE_TYPE)" >&2

# Construct a new output path.
# In this example, we'll pretend we uploaded it to a provider called "mycloud".
OUTPUT_PATH="mycloud://$SERVICE_TYPE/$(basename "$INPUT_PATH")"

# --- End of Custom Logic ---

# The final step is to output a JSON object containing the new output_path.
# This will be read by the plugNmeet server and stored in the database.
jq -n --arg output_path "$OUTPUT_PATH" '{"output_path": $output_path}'
