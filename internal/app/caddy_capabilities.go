package app

import (
	"path"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
)

func caddyAdminReason(install monitor.CaddyInstall) string {
	if install.AdminAddr == "off" {
		return locales.CpanelAdminOff()
	}
	if !caddyServiceRunning(install) {
		return locales.SvcWrongState()
	}
	return ""
}

func caddyValidateReason(install monitor.CaddyInstall) string {
	if !monitor.CaddyConfigPreviewSupported(install) {
		return locales.CwireCannotValidate()
	}
	return ""
}

func caddyConfigWriteReason(install monitor.CaddyInstall, file string) string {
	if !monitor.CaddyConfigWritable(install, file) {
		return locales.CwireCannotWrite()
	}
	if reason := caddyValidateReason(install); reason != "" {
		return reason
	}
	if caddyServiceRunning(install) && install.AdminAddr == "off" {
		return locales.CpanelAdminOff()
	}
	return ""
}

func caddyUpdateReason(install monitor.CaddyInstall, repair bool) string {
	if install.Method == monitor.CaddyDocker && (install.ContainerCompose || install.ContainerAutoRemove || strings.Contains(install.ContainerImage, "@")) {
		return locales.CwireDockerLifecycle()
	}
	if !monitor.CaddyCanUpdate(install) {
		return locales.CwireCannotUpdate()
	}
	if repair && (install.Method != monitor.CaddyBinary && install.Method != monitor.CaddyDocker || install.Method == monitor.CaddyBinary && (len(install.Modules) > 0 || caddyVersionNumber.FindString(install.Version) == "") || install.Method == monitor.CaddyDocker && install.ContainerImage == "") {
		return locales.CwireNoRepair()
	}
	return ""
}

func caddyUninstallReason(install monitor.CaddyInstall, purge bool) string {
	if install.Method == monitor.CaddyDocker && (install.ContainerCompose || install.ContainerAutoRemove) {
		return locales.CwireDockerLifecycle()
	}
	if purge && (install.Method == monitor.CaddyDocker || install.Config != "/etc/caddy/Caddyfile" || install.DataDir != "/var/lib/caddy/.local/share/caddy") {
		return locales.CwireNoPurge()
	}
	if install.Method != monitor.CaddyDocker && install.ServiceManager != "systemd" && install.ServiceManager != "openrc" && (caddyServiceRunning(install) || len(install.Listening) != 0) ||
		install.Method == monitor.CaddyBinary && (!caddyValidPath(install.Binary) || path.Base(install.Binary) != "caddy") {
		return locales.CpanelNoTarget()
	}
	return ""
}
