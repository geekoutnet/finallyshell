//go:build !windows

package app

import (
	"context"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
)

func tunnelLANFirewallStatus(context.Context, int) string { return locales.TexpLANFirewallOther() }

func addTunnelLANFirewallRule(int) error { return monitor.ErrOpsInvalid }
