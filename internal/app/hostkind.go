package app

// How a host's kind, SSH or Remote Desktop, shows wherever hosts are
// listed. One place decides it, kindLook, so that a host looks the same
// everywhere.

import (
	"slices"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// hostKindLook is how a kind shows: its icon, the short label of its
// pill, and the color of both.
type hostKindLook struct {
	icon  string
	label string
	color ui.Color
}

// kindLook is how h's kind shows.
func kindLook(h *config.Host) hostKindLook {
	sshColor, rdpColor := ui.Hex("#047857"), ui.Hex("#1d4ed8")
	if theme.Dark {
		sshColor, rdpColor = ui.Hex("#6ee7b7"), ui.Hex("#93c5fd")
	}
	if h.IsRDP() {
		return hostKindLook{icon: "monitor", label: locales.KindRDPShort(), color: rdpColor}
	}
	return hostKindLook{icon: "square-terminal", label: locales.KindSSH(), color: sshColor}
}

// hostIcon uses the kind's color, or the text color on a chosen row.
func hostIcon(c *ui.Context, h *config.Host, chosen bool, color ui.Color) {
	look := kindLook(h)
	if chosen {
		look.color = color.Alpha(0.85)
	}
	ui.Icon(c, icons.Get(look.icon)).Label(look.label).FontSize(14).TextColor(look.color).Shrink(0)
}

// kindPill names the kind in its color, or the text color on a chosen row.
func kindPill(c *ui.Context, h *config.Host, chosen bool, color ui.Color) {
	look := kindLook(h)
	if chosen {
		look.color = color
	}
	ui.Text(c, look.label).FontSize(11).SingleLine().Shrink(0).Padding(1, 5).Radius(4).
		TextColor(look.color).Background(look.color.Alpha(0.14))
}

// kindFilter is which kinds of hosts a tree shows.
type kindFilter int

const (
	kindsAll kindFilter = iota
	kindsSSH
	kindsRDP
)

// keeps tells whether the filter shows h.
func (k kindFilter) keeps(h *config.Host) bool {
	switch k {
	case kindsSSH:
		return !h.IsRDP()
	case kindsRDP:
		return h.IsRDP()
	}
	return true
}

// kindFilterLabels are the labels of the filter's choices, in its
// order; short for a narrow place, as the Connections page.
func kindFilterLabels(short bool) []string {
	rdp := locales.RemoteDesktop()
	if short {
		rdp = locales.KindRDPShort()
	}
	return []string{locales.AllTypes(), locales.KindSSH(), rdp}
}

// matchesKind recognizes kind keywords in either supported language.
func matchesKind(h *config.Host, word string) bool {
	if h.IsRDP() {
		return slices.Contains([]string{"rdp", "remote", "desktop", "远程桌面", "远程", "桌面"}, word) ||
			strings.EqualFold(word, locales.KindRDPShort())
	}
	return word == "ssh"
}
