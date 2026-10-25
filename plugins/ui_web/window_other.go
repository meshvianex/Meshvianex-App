//go:build !windows

package ui_web

import "context"

func WindowSupported() bool { return false }

func OpenWindow(context.Context, string, string) error { return nil }
