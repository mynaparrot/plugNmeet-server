package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/sirupsen/logrus"
)

// StorageHooks defines optional script pipelines for handling file I/O.
// If this block is not present in the config, the application defaults to the local filesystem.
type StorageHooks struct {
	// A list of scripts to execute sequentially for an upload operation.
	UploadHook []string `yaml:"upload_hook"`
	// A list of scripts for a download operation.
	DownloadHook []string `yaml:"download_hook"`
	// A list of scripts for a delete operation.
	DeleteHook []string `yaml:"delete_hook"`
}

// UploadHookRequest is the JSON payload sent to the *first* script in the upload pipeline.
type UploadHookRequest struct {
	SourceFilePath string `json:"source_file_path"`
	LogicalPath    string `json:"logical_path,omitempty"`
	ServiceType    string `json:"service_type"`
	RoomId         string `json:"room_id"`
	RoomSid        string `json:"room_sid"`
	RoomTableId    uint64 `json:"room_table_id"`
}

// UploadHookResponse is the JSON payload expected from the *last* script in the upload pipeline.
type UploadHookResponse struct {
	Error       string `json:"error,omitempty"`
	LogicalPath string `json:"logical_path,omitempty"`
}

// DownloadHookRequest is the JSON payload sent to the *first* script in the download pipeline.
type DownloadHookRequest struct {
	LogicalPath string `json:"logical_path"`
	ServiceType string `json:"service_type"`
}

// DownloadHookResponse is the JSON payload expected from the *last* script in the download pipeline.
type DownloadHookResponse struct {
	Error       string `json:"error,omitempty"`
	Action      string `json:"action,omitempty"`
	RedirectUrl string `json:"redirect_url,omitempty"`
	LocalPath   string `json:"local_path,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
}

// DeleteHookRequest is the JSON payload sent to the delete hook pipeline.
type DeleteHookRequest struct {
	LogicalPath string `json:"logical_path"`
	ServiceType string `json:"service_type"`
}

// DeleteHookResponse is the JSON payload expected from the delete hook pipeline.
type DeleteHookResponse struct {
	Error string `json:"error,omitempty"`
	Msg   string `json:"msg,omitempty"`
}

// ExecuteHookPipeline runs a series of scripts, passing data from one to the next.
func ExecuteHookPipeline(ctx context.Context, scripts []string, initialData interface{}, log *logrus.Entry) (json.RawMessage, error) {
	jsonData, err := json.Marshal(initialData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal initial data for hook script: %w", err)
	}

	for _, script := range scripts {
		log.Infof("Running storage hook script: %s", script)

		cmd := exec.CommandContext(ctx, script)
		cmd.Stdin = bytes.NewReader(jsonData)
		var out bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("storage hook script %s failed: %w, stderr: %s", script, err, stderr.String())
		}

		if len(bytes.TrimSpace(out.Bytes())) > 0 {
			if !json.Valid(out.Bytes()) {
				return nil, fmt.Errorf("storage hook script %s returned invalid JSON: %s", script, out.String())
			}
			jsonData = out.Bytes()
		}
	}

	return jsonData, nil
}
