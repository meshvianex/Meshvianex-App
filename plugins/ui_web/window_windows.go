//go:build windows

package ui_web

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func WindowSupported() bool { return true }

// OpenWindow uses Chromium's app mode, which gives the dashboard its own
// window without browser tabs, address bars, or a manual browser step.
func OpenWindow(ctx context.Context, url, _ string) error {
	executable, err := findChromiumBrowser()
	if err != nil {
		return err
	}
	profileDir, err := os.MkdirTemp("", "meshvianex-ui-")
	if err != nil {
		return fmt.Errorf("create UI window profile: %w", err)
	}
	defer os.RemoveAll(profileDir)
	cmd := exec.Command(executable, "--app="+url, "--new-window", "--user-data-dir="+profileDir)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start UI window: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		return nil
	case err := <-done:
		if err != nil {
			return fmt.Errorf("UI window exited: %w", err)
		}
		return nil
	}
}

func findChromiumBrowser() (string, error) {
	for _, name := range []string{"msedge.exe", "chrome.exe", "brave.exe", "chromium.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	programFiles := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")}
	relativePaths := []string{
		`Microsoft\Edge\Application\msedge.exe`,
		`Google\Chrome\Application\chrome.exe`,
		`BraveSoftware\Brave-Browser\Application\brave.exe`,
		`Chromium\Application\chrome.exe`,
	}
	for _, root := range programFiles {
		if root == "" {
			continue
		}
		for _, relative := range relativePaths {
			path := filepath.Join(root, relative)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("no Edge, Chrome, Brave, or Chromium installation was found for the Meshvianex UI")
}
