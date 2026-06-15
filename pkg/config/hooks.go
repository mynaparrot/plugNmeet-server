package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cavaliergopher/grab/v3"
	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/sirupsen/logrus"
)

// InitializeHooks sets up the entire hook system for the server.
// It resolves script paths, validates them, and starts the long-lived processes
// for all unique, non-one-shot scripts defined in the configuration.
func InitializeHooks(ctx context.Context, appCnf *AppConfig) error {
	if appCnf.Hooks == nil {
		return nil // Feature is not enabled.
	}

	resolvePath := func(scriptPath string) string {
		if !filepath.IsAbs(scriptPath) {
			p := filepath.Join(appCnf.RootWorkingDir, scriptPath)
			return filepath.Clean(p)
		}
		return scriptPath
	}

	// scriptsWithPoolSize maps each unique script path to its required pool size.
	// We take the max pool size if a script is used in multiple categories.
	scriptsWithPoolSize := make(map[string]int)

	processHookCategory := func(config *hooks.HookScriptConfig, name string) error {
		if config == nil {
			return nil
		}
		if config.PoolSize <= 0 {
			config.PoolSize = 1
		}
		if config.HookTimeout == 0 {
			config.HookTimeout = 5 * time.Minute
		}
		for i, script := range config.Scripts {
			var resolved string
			if strings.HasPrefix(script.Script, hooks.HookCommandHttpRequest) {
				resolved = script.Script
			} else {
				resolved = resolvePath(script.Script)
			}

			if err := hooks.ValidateHookScript(resolved, name); err != nil {
				return err
			}
			config.Scripts[i].Script = resolved

			if !script.IsOneShot {
				// If the same script is used in multiple hooks, use the larger pool size.
				if currentSize, ok := scriptsWithPoolSize[resolved]; !ok || config.PoolSize > currentSize {
					scriptsWithPoolSize[resolved] = config.PoolSize
				}
			}
		}
		return nil
	}

	if err := processHookCategory(appCnf.Hooks.UploadHook, "upload_hook"); err != nil {
		return err
	}
	if err := processHookCategory(appCnf.Hooks.DownloadHook, "download_hook"); err != nil {
		return err
	}
	if err := processHookCategory(appCnf.Hooks.DeleteHook, "delete_hook"); err != nil {
		return err
	}
	if err := processHookCategory(appCnf.Hooks.ResumableUploadHook, "resumable_upload_hook"); err != nil {
		return err
	}
	if err := processHookCategory(appCnf.Hooks.RoomEndHook, "room_end_hook"); err != nil {
		return err
	}

	// Initialize the HookProcessManager and start all unique scripts
	appCnf.HookManager = hooks.NewHookProcessManager(ctx, appCnf.Logger.WithField("service", "hook_manager"))
	if err := appCnf.HookManager.StartHookProcesses(scriptsWithPoolSize); err != nil {
		return fmt.Errorf("failed to start hook processes: %w", err)
	}

	return nil
}

// RunUploadHook executes the pipeline for uploading files.
// It sends the UploadHookData to the configured scripts and returns the final, modified data.
// Returns nil if no upload hooks are configured.
func (h *Hooks) RunUploadHook(manager *hooks.HookProcessManager, req *hooks.UploadHookData, log *logrus.Entry) (*hooks.UploadHookData, error) {
	if manager == nil || h.UploadHook == nil || len(h.UploadHook.Scripts) == 0 {
		return nil, nil
	}

	resBytes, err := hooks.ExecuteHookPipeline(manager, h.UploadHook.Scripts, &req, h.UploadHook.HookTimeout, log)
	if err != nil {
		return nil, fmt.Errorf("upload hook pipeline failed with error: %w", err)
	}

	// script will return same struct
	var res hooks.UploadHookData
	if err := json.Unmarshal(resBytes, &res); err != nil {
		return nil, fmt.Errorf("failed to unmarshal upload hook response error: %w", err)
	}

	if res.Error != "" {
		return nil, fmt.Errorf("upload hook script returned an error: %s", res.Error)
	}

	log.Infof("Upload hook successful proceeded with output %s ", res.OutputPath)
	return &res, nil
}

// RunDownloadHook executes the pipeline for downloading files.
// It sends DownloadHookData to the scripts. If a localDownloadDirPath is provided,
// it will also attempt to download the file from the final redirect URL to that directory.
// Returns nil if no download hooks are configured.
func (h *Hooks) RunDownloadHook(ctx context.Context, manager *hooks.HookProcessManager, req *hooks.DownloadHookData, localDownloadDirPath *string, downloadTimeout time.Duration, log *logrus.Entry) (*hooks.DownloadHookData, error) {
	if manager == nil || h.DownloadHook == nil || len(h.DownloadHook.Scripts) == 0 {
		return nil, nil
	}

	resBytes, err := hooks.ExecuteHookPipeline(manager, h.DownloadHook.Scripts, &req, h.DownloadHook.HookTimeout, log)
	if err != nil {
		return nil, fmt.Errorf("download hook pipeline failed with error: %w", err)
	}

	// script will return same struct
	var res hooks.DownloadHookData
	if err := json.Unmarshal(resBytes, &res); err != nil {
		return nil, fmt.Errorf("failed to unmarshal download hook response error: %w", err)
	}

	if res.Error != "" {
		return nil, fmt.Errorf("download hook script returned an error: %s", res.Error)
	}

	if localDownloadDirPath != nil {
		if res.RedirectUrl != "" {
			if err := os.MkdirAll(*localDownloadDirPath, 0755); err != nil {
				return nil, err
			}
			filePath, err := h.downloadFileToDest(ctx, res.RedirectUrl, *localDownloadDirPath, downloadTimeout, log)
			if err != nil {
				return nil, err
			}
			res.OutputPath = filePath
		}
	}

	return &res, nil
}

// RunDeleteHook executes the pipeline for deleting files.
// It sends DeleteHookData to the configured scripts to handle deletion from external storage.
// Returns nil if no delete hooks are configured.
func (h *Hooks) RunDeleteHook(manager *hooks.HookProcessManager, req *hooks.DeleteHookData, log *logrus.Entry) (*hooks.DeleteHookData, error) {
	if manager == nil || h.DeleteHook == nil || len(h.DeleteHook.Scripts) == 0 {
		return nil, nil
	}

	resBytes, err := hooks.ExecuteHookPipeline(manager, h.DeleteHook.Scripts, &req, h.DeleteHook.HookTimeout, log)
	if err != nil {
		return nil, fmt.Errorf("delete hook pipeline failed with error: %w", err)
	}

	var res hooks.DeleteHookData
	if err := json.Unmarshal(resBytes, &res); err != nil {
		return nil, fmt.Errorf("failed to unmarshal delete hook response error: %w", err)
	}

	if res.Error != "" {
		return nil, fmt.Errorf("download hook script returned an error: %s", res.Error)
	}

	return &res, nil
}

// downloadFileToDest uses the 'grab' library to download a file from a URL to a destination directory.
// It respects the provided context and timeout.
// It returns the full path to the downloaded file upon success.
func (h *Hooks) downloadFileToDest(ctx context.Context, fileUrl, dstDir string, timeout time.Duration, log *logrus.Entry) (fileFullPath string, err error) {
	log = log.WithField("sub-method", "downloadFileToDest")
	if timeout == 0 {
		timeout = time.Minute * 3
	}

	client := grab.NewClient()
	req, err := grab.NewRequest(dstDir, fileUrl)
	if err != nil {
		return "", fmt.Errorf("failed to create download request: %w", err)
	}

	gctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp := client.Do(req.WithContext(gctx))
	<-resp.Done

	if err := resp.Err(); err != nil {
		return "", fmt.Errorf("failed to download file: %w", err)
	}

	return resp.Filename, nil
}

// RunResumableUploadHook executes the pipeline for uploading files.
// It sends the ResumableUploadHookData to the configured scripts and returns the final, modified data.
// Returns nil if no upload hooks are configured.
func (h *Hooks) RunResumableUploadHook(manager *hooks.HookProcessManager, req *hooks.ResumableUploadHookData, log *logrus.Entry) (*hooks.ResumableUploadHookData, error) {
	if manager == nil || h.ResumableUploadHook == nil || len(h.ResumableUploadHook.Scripts) == 0 {
		return nil, nil
	}

	resBytes, err := hooks.ExecuteHookPipeline(manager, h.ResumableUploadHook.Scripts, req, h.ResumableUploadHook.HookTimeout, log)
	if err != nil {
		return nil, err
	}

	var res hooks.ResumableUploadHookData
	if err := json.Unmarshal(resBytes, &res); err != nil {
		return nil, fmt.Errorf("failed to unmarshal resumable upload hook response: %w", err)
	}

	if res.Error != "" {
		return nil, fmt.Errorf("resumable upload hook script returned an error: %s", res.Error)
	}

	return &res, nil
}
