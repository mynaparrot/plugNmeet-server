package config

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	clientDownloadTimeout = time.Minute // 1-minute timeout for client download
)

type AutoClientDownload struct {
	Enabled       bool   `yaml:"enabled"`
	ServerUrl     string `yaml:"server_url"`
	FromCustomUrl string `yaml:"from_custom_url"`
	FromRelease   string `yaml:"from_release"`
}

// Handle is the main entry point to trigger the client download process.
func (d *AutoClientDownload) Handle(a *AppConfig) error {
	if d == nil || !d.Enabled {
		return nil
	}

	logrus.Infoln("auto client download enabled")
	if d.ServerUrl == "" {
		return fmt.Errorf("`server_url` in `auto_client_download` is required when enabled")
	}

	if strings.Contains(d.ServerUrl, "localhost") {
		if !a.Client.Debug {
			return fmt.Errorf("`server_url` in `auto_client_download` cannot contain 'localhost' when debug mode is disabled")
		}
		// If debug is true, just log a warning
		logrus.Warnln("`server_url` contains 'localhost' in debug mode. This is not recommended for production.")
	}

	// First, determine the final absolute path for the client
	clientPath := a.Client.Path
	if strings.HasPrefix(clientPath, "./") {
		clientPath = filepath.Join(a.RootWorkingDir, clientPath)
	}

	// Determine the target version
	targetVersion := d.FromRelease
	if d.FromCustomUrl == "" && targetVersion != "" {
		// We have a specific version, let's check if it's already installed.
		versionFile := filepath.Join(clientPath, "version.txt")
		if _, err := os.Stat(versionFile); err == nil {
			content, err := os.ReadFile(versionFile)
			if err == nil {
				installedVersion := strings.TrimSpace(string(content))
				if installedVersion == targetVersion {
					logrus.Infof("client version %s is already installed. skipping download.", targetVersion)
					return nil
				}
			}
		}
	}

	downloadUrl := d.FromCustomUrl
	if downloadUrl == "" {
		version := d.FromRelease
		if version == "" {
			version = "latest"
		}
		downloadUrl = fmt.Sprintf("https://github.com/mynaparrot/plugNmeet-client/releases/download/%s/client.zip", version)
	}

	// Ensure the parent directory exists
	parentDir := filepath.Dir(clientPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory for client: %w", err)
	}

	// Create a temporary directory *on the same volume* as the final destination
	tempDir, err := os.MkdirTemp(parentDir, ".client-download-")
	if err != nil {
		return err
	}
	// Defer removal in case of error, but we will rename it on success
	defer os.RemoveAll(tempDir)

	logrus.Infof("downloading client from %s to %s", downloadUrl, tempDir)
	zipFile := filepath.Join(tempDir, "client.zip")
	err = d.downloadFile(zipFile, downloadUrl)
	if err != nil {
		return fmt.Errorf("failed to download client: %w", err)
	}

	err = d.unzip(zipFile, tempDir)
	if err != nil {
		return fmt.Errorf("failed to unzip client: %w", err)
	}

	distPath := filepath.Join(tempDir, "client", "dist")
	if _, err := os.Stat(distPath); os.IsNotExist(err) {
		return fmt.Errorf("`client/dist` folder not found in the downloaded client archive")
	}

	// Configure config.js inside the temporary dist path
	configSample := filepath.Join(distPath, "assets", "config_sample.js")
	configJs := filepath.Join(distPath, "assets", "config.js")

	input, err := os.ReadFile(configSample)
	if err != nil {
		return fmt.Errorf("failed to read config_sample.js: %w", err)
	}

	re := regexp.MustCompile(`serverUrl:\s*'.*?'`)
	newConfig := re.ReplaceAllString(string(input), fmt.Sprintf("serverUrl: '%s'", d.ServerUrl))

	err = os.WriteFile(configJs, []byte(newConfig), 0644)
	if err != nil {
		return fmt.Errorf("failed to write config.js: %w", err)
	}

	// This is the atomic part of the operation.
	if err = os.RemoveAll(clientPath); err != nil {
		return fmt.Errorf("failed to remove old client directory: %w", err)
	}

	if err = os.Rename(distPath, clientPath); err != nil {
		return fmt.Errorf("failed to atomically move new client directory: %w", err)
	}

	os.Remove(zipFile)
	os.Remove(filepath.Join(tempDir, "client"))

	logrus.Infof("successfully downloaded and configured client to %s", clientPath)
	return nil
}

func (d *AutoClientDownload) downloadFile(filepath string, url string) error {
	client := http.Client{
		Timeout: clientDownloadTimeout,
	}

	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download file: status %s", resp.Status)
	}

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func (d *AutoClientDownload) unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	os.MkdirAll(dest, 0755)

	for _, f := range r.File {
		err := d.extractAndWriteFile(f, dest)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *AutoClientDownload) extractAndWriteFile(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	path := filepath.Join(dest, f.Name)

	if f.FileInfo().IsDir() {
		os.MkdirAll(path, f.Mode())
	} else {
		os.MkdirAll(filepath.Dir(path), f.Mode())
		df, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		defer df.Close()

		_, err = io.Copy(df, rc)
		if err != nil {
			return err
		}
	}
	return nil
}
