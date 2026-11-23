//go:build !windows

package main

import "errors"

// installerName is "" where no setup program installs the app: an update
// is downloaded by hand from its release page.
func installerName(version string) string { return "" }

func canInstallUpdate() bool { return false }

func startInstall(setup string) error { return errors.ErrUnsupported }
