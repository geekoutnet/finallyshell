package app

import (
	"cmp"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type dockerNetworksPage struct {
	dockerResourceLoad[[]monitor.DockerNetworkDetails]
	table dockerResourceTable
}

func (a *App) loadDockerNetworks(s *Session, page *dockerResourcesPage, force bool) {
	cmd := page.tool
	readDockerResource(a, s, &page.networks.dockerResourceLoad, force, resourceInspect(cmd, "network", cmd.NetworkList(), monitor.ParseDockerNetworkDetails))
}

func networkCols() []ui.TableColumn {
	return []ui.TableColumn{
		resourceColumn("name", locales.FieldName(), 140), resourceColumn("driver", locales.DockerResDriver(), 90),
		resourceColumn("scope", locales.DockerResScope(), 75), resourceColumn("subnet", locales.DockerResSubnet(), 170),
		resourceColumn("gateway", locales.DockerResGateway(), 145), resourceColumn("endpoints", locales.DockerContainers(), 95),
		resourceColumn("flags", locales.DockerResFlags(), 145), resourceColumn("created", locales.DockerCreated(), 155),
	}
}

func networkMembers(network monitor.DockerNetworkDetails, docker *monitor.Docker) []monitor.DockerNetworkEndpoint {
	rows := slices.Clone(network.Endpoints)
	for _, container := range docker.Containers {
		if slices.ContainsFunc(rows, func(endpoint monitor.DockerNetworkEndpoint) bool { return endpoint.ID == container.ID }) {
			continue
		}
		for _, link := range container.Networks {
			if link.Name == network.Name {
				rows = append(rows, monitor.DockerNetworkEndpoint{ID: container.ID, Name: container.Name, IPv4: withPrefix(link.IP, link.PrefixLen), IPv6: withPrefix(link.IPv6, link.IPv6PrefixLen), MAC: link.MAC})
				break
			}
		}
	}
	slices.SortFunc(rows, func(left, right monitor.DockerNetworkEndpoint) int { return strings.Compare(left.Name, right.Name) })
	return rows
}

func networkRows(networks []monitor.DockerNetworkDetails, docker *monitor.Docker) []dockerResourceRow {
	var rows []dockerResourceRow
	for _, network := range networks {
		var flags []string
		if network.Internal {
			flags = append(flags, locales.DockerResInternal())
		}
		if network.Attachable {
			flags = append(flags, locales.DockerResAttachable())
		}
		members := len(networkMembers(network, docker))
		rows = append(rows, dockerResourceRow{network.ID, network.Name, []string{
			network.Name, network.Driver, network.Scope, networkSubnetValues(network, false), networkSubnetValues(network, true),
			strconv.Itoa(members), strings.Join(flags, ", "), localTime(network.Created),
		}, map[string]int64{"endpoints": int64(members), "created": network.Created.Unix()}})
	}
	return rows
}

func networkSubnetValues(network monitor.DockerNetworkDetails, gateway bool) string {
	var values []string
	for _, subnet := range network.Subnets {
		value := subnet.Subnet
		if gateway {
			value = subnet.Gateway
		}
		if value != "" {
			values = append(values, value)
		}
	}
	return strings.Join(values, ", ")
}

func (a *App) dockerNetworksSection(c *ui.Context, s *Session) {
	page := a.dockerResources(s)
	section := &page.networks
	a.loadDockerNetworks(s, page, false)
	cols := networkCols()
	rows := section.table.shown(networkRows(section.rows, &s.overview.docker.d), cols)
	menu := func(m *ui.Menu, key string) { a.networkMenu(c, m, s, page, key) }
	details := func(key string) { a.openNetworkDetails(s, page, key) }
	if a.panelOnRight() && !a.current().isPanelTab() {
		resourceTitle(c, locales.DockerNetworks())
	}
	resourceToolbar(c, &section.table, len(rows), func() {
		if ui.PrimaryButton(c, locales.DockerResCreate()).Disabled(page.blocked(s) != "").Shrink(0).Clicked() {
			a.createDockerNetwork(s, page)
		}
		reason := cmp.Or(page.blocked(s), resourceListReason(&section.dockerResourceLoad))
		if reason == "" && len(networkCandidates(s, section.rows)) == 0 {
			reason = locales.DockerResNoCandidates()
		}
		if ui.Button(c, locales.DockerResPrune()).Disabled(reason != "").Tooltip(reason).Shrink(0).Clicked() {
			a.confirmNetworkRemove(s, page, networkCandidates(s, section.rows), true)
		}
		ui.Button(c, locales.DockerResManage()).Disabled(section.table.selected.Len() == 0 && page.lastWork == nil).Shrink(0).Menu(func(m *ui.Menu) {
			page.outputMenu(m)
			for _, row := range rows {
				if section.table.selected.Has(row.key) {
					menu(m, row.key)
					break
				}
			}
		})
	})
	resourceStatus(c, &section.dockerResourceLoad, func() { a.loadDockerNetworks(s, page, true) })
	section.table.draw(c, locales.DockerNetworks(), cols, rows, menu, details)
	page.drawDialog(c)
}

func lookupNetwork(s *Session, page *dockerResourcesPage, key string) (monitor.DockerNetworkDetails, bool) {
	for _, row := range page.networks.rows {
		if row.ID == key {
			row.Endpoints = networkMembers(row, &s.overview.docker.d)
			return row, true
		}
	}
	return monitor.DockerNetworkDetails{}, false
}

func networkRemoveReason(network monitor.DockerNetworkDetails) string {
	if network.Protected() {
		return locales.DockerResProtected()
	}
	if len(network.Endpoints) > 0 {
		return locales.DockerResAttached()
	}
	if !monitor.ValidName(network.ID) {
		return locales.DockerResStale()
	}
	return ""
}

func networkCandidates(s *Session, networks []monitor.DockerNetworkDetails) []monitor.DockerNetworkDetails {
	var selected []monitor.DockerNetworkDetails
	for _, network := range networks {
		if network.Scope == "swarm" || network.Scope == "global" || len(networkMembers(network, &s.overview.docker.d)) != 0 {
			continue
		}
		if networkRemoveReason(network) == "" {
			selected = append(selected, network)
		}
	}
	return selected
}

func (a *App) networkMenu(c *ui.Context, m *ui.Menu, s *Session, page *dockerResourcesPage, key string) {
	network, found := lookupNetwork(s, page, key)
	if !found {
		return
	}
	if m.Item(locales.DockerDetails()).Chosen() {
		a.openNetworkDetails(s, page, key)
	}
	base := cmp.Or(page.blocked(s), resourceListReason(&page.networks.dockerResourceLoad))
	connect, disconnect := networkEndpointReasons(s, network, base)
	if resourceMenuItem(m, locales.DockerResConnect(), connect, false) {
		a.openNetworkEndpoint(s, page, network, false)
	}
	if resourceMenuItem(m, locales.DockerResDisconnect(), disconnect, false) {
		a.openNetworkEndpoint(s, page, network, true)
	}
	resourceCopy(c, m, labelValue{locales.FieldName(), network.Name}, labelValue{locales.DockerID(), network.ID}, labelValue{locales.DockerResSubnet(), networkSubnetValues(network, false)})
	m.Separator()
	chosen := []monitor.DockerNetworkDetails{network}
	if page.networks.table.selected.Has(key) {
		for other := range page.networks.table.selected.All() {
			if other == key {
				continue
			}
			if item, ok := lookupNetwork(s, page, other); ok {
				chosen = append(chosen, item)
			}
		}
	}
	reason := base
	for _, item := range chosen {
		reason = cmp.Or(reason, networkRemoveReason(item))
	}
	if resourceMenuItem(m, locales.DockerResNetRemove(), reason, true) {
		a.confirmNetworkRemove(s, page, chosen, false)
	}
}

func networkEndpointReasons(s *Session, network monitor.DockerNetworkDetails, base string) (string, string) {
	connect, disconnect := base, base
	if network.Protected() && network.Name != "bridge" && network.Name != "podman" {
		connect, disconnect = cmp.Or(base, locales.DockerResProtected()), cmp.Or(base, locales.DockerResProtected())
	}
	if len(networkEndpointChoices(s, network, false)) == 0 {
		connect = cmp.Or(connect, locales.DockerResNoneToConnect())
	}
	if len(network.Endpoints) == 0 {
		disconnect = cmp.Or(disconnect, locales.DockerResNoneToDisconnect())
	}
	return connect, disconnect
}

func (a *App) openNetworkDetails(s *Session, page *dockerResourcesPage, key string) {
	page.show(locales.DockerDetails(), func(c *ui.Context, _ *dockerResourceDialog, _ *bool) {
		network, found := lookupNetwork(s, page, key)
		if !found {
			ui.Text(c, locales.DockerResStale())
			return
		}
		ui.Text(c, network.Name).Bold().Selectable()
		ctrRows(c, []labelValue{
			{locales.DockerID(), network.ID}, {locales.DockerResDriver(), network.Driver}, {locales.DockerResScope(), network.Scope},
			{locales.DockerCreated(), localTime(network.Created)}, {locales.DockerResInternal(), yesNo(network.Internal)},
			{locales.DockerResAttachable(), yesNo(network.Attachable)}, {locales.DockerResIngress(), yesNo(network.Ingress)},
			{locales.DockerResIPv6(), yesNo(network.IPv6)}, {"IPAM", network.IPAMDriver}, {"IPAM · " + locales.DockerResOptions(), resourcePairText(network.IPAMOptions)},
			{locales.DockerSecLabels(), resourcePairText(network.Labels)}, {locales.DockerResOptions(), resourcePairText(network.Options)},
		}, 130)
		for _, subnet := range network.Subnets {
			ctrSection(c, subnet.Subnet, nil, func() {
				ctrRows(c, []labelValue{{locales.DockerResGateway(), subnet.Gateway}, {locales.DockerResRange(), subnet.IPRange}}, 130)
			})
		}
		ui.Text(c, locales.DockerContainers()).Bold()
		for _, endpoint := range network.Endpoints {
			ctrSection(c.Key(endpoint.ID), cmp.Or(endpoint.Name, endpoint.ID), nil, func() {
				ctrRows(c, []labelValue{{locales.DockerID(), endpoint.ID}, {locales.DockerIPv4(), endpoint.IPv4}, {locales.DockerIPv6(), endpoint.IPv6}, {locales.DockerMAC(), endpoint.MAC}}, 130)
			})
		}
		connect, disconnect := networkEndpointReasons(s, network, cmp.Or(page.blocked(s), resourceListReason(&page.networks.dockerResourceLoad)))
		ui.Row(c).Gap(6).Wrap().Children(func() {
			if ui.Button(c, locales.DockerResConnect()).Disabled(connect != "").Tooltip(connect).Clicked() {
				a.openNetworkEndpoint(s, page, network, false)
			}
			if ui.Button(c, locales.DockerResDisconnect()).Disabled(disconnect != "").Tooltip(disconnect).Clicked() {
				a.openNetworkEndpoint(s, page, network, true)
			}
		})
	})
}

func (a *App) createDockerNetwork(s *Session, page *dockerResourcesPage) {
	spec := monitor.DockerNetworkSpec{Driver: "bridge"}
	driver, custom := "bridge", ""
	page.show(locales.DockerResNewNet(), func(c *ui.Context, dialog *dockerResourceDialog, open *bool) {
		resourceText(c, locales.FieldName(), &spec.Name)
		drivers := []string{"bridge", "macvlan", "ipvlan"}
		if page.tool.Tool == "docker" {
			drivers = append(drivers, "overlay")
		}
		drivers = append(drivers, locales.DockerResCustomDriver())
		ui.Text(c, locales.DockerResDriver()).FontSize(12)
		ui.Select(c, &driver, drivers).Label(locales.DockerResDriver()).FillWidth()
		spec.Driver = driver
		if driver == locales.DockerResCustomDriver() {
			resourceText(c, locales.DockerResDriver(), &custom)
			spec.Driver = strings.TrimSpace(custom)
		}
		networkSubnetFields(c.Key("ipv4"), "IPv4", &spec.IPv4)
		ui.Row(c).Gap(8).Wrap().Children(func() {
			ui.Checkbox(c, &spec.Internal, locales.DockerResInternal())
			ui.Checkbox(c, &spec.Attachable, locales.DockerResAttachable()).Disabled(page.tool.Tool == "podman").Tooltip(locales.DockerResPodman())
			ui.Checkbox(c, &spec.EnableIPv6, locales.DockerResIPv6())
		})
		if spec.EnableIPv6 {
			networkSubnetFields(c.Key("ipv6"), "IPv6", &spec.IPv6)
		}
		resourceArea(c, locales.DockerResOptions(), &spec.Options, 64)
		resourceArea(c, locales.DockerResLabels(), &spec.Labels, 64)
		input := spec
		input.Name = strings.TrimSpace(input.Name)
		for _, subnet := range []*monitor.DockerSubnet{&input.IPv4, &input.IPv6} {
			subnet.Subnet, subnet.Gateway, subnet.IPRange = strings.TrimSpace(subnet.Subnet), strings.TrimSpace(subnet.Gateway), strings.TrimSpace(subnet.IPRange)
		}
		if !input.EnableIPv6 {
			input.IPv6 = monitor.DockerSubnet{}
		}
		script, err := page.tool.NetworkCreate(input)
		reason := page.blocked(s)
		if err != nil {
			reason = cmp.Or(reason, locales.DockerResInvalid())
			if spec.Name != "" {
				ui.Text(c, reason).TextColor(theme.Danger)
			}
		}
		if resourceButtons(c, open, locales.DockerResCreate(), reason) {
			a.runDockerResource(s, page, dialog, dockerResourceStep{script: script})
		}
	})
}

func networkSubnetFields(c *ui.Context, family string, subnet *monitor.DockerSubnet) {
	ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
		for _, field := range []struct {
			label string
			value *string
		}{{locales.DockerResSubnet(), &subnet.Subnet}, {locales.DockerResGateway(), &subnet.Gateway}, {locales.DockerResRange(), &subnet.IPRange}} {
			ui.Column(c.Key(field.label)).Grow(1).MinWidth(130).Children(func() {
				label := family + " · " + field.label
				ui.Text(c, label).FontSize(12)
				ui.TextInput(c, field.value).Label(label).FillWidth()
			})
		}
	})
}

func networkEndpointChoices(s *Session, network monitor.DockerNetworkDetails, disconnect bool) []monitor.DockerNetworkEndpoint {
	if disconnect {
		return slices.DeleteFunc(slices.Clone(network.Endpoints), func(endpoint monitor.DockerNetworkEndpoint) bool { return !monitor.ValidName(endpoint.ID) })
	}
	var choices []monitor.DockerNetworkEndpoint
	for _, container := range s.overview.docker.d.Containers {
		if !monitor.ValidName(container.ID) || container.Status == "removing" || container.Status == "dead" || strings.HasPrefix(container.NetworkMode, "container:") || container.NetworkMode == "host" {
			continue
		}
		if !slices.ContainsFunc(network.Endpoints, func(endpoint monitor.DockerNetworkEndpoint) bool { return endpoint.ID == container.ID }) {
			choices = append(choices, monitor.DockerNetworkEndpoint{ID: container.ID, Name: container.Name})
		}
	}
	slices.SortFunc(choices, func(left, right monitor.DockerNetworkEndpoint) int { return strings.Compare(left.Name, right.Name) })
	return choices
}

func (a *App) openNetworkEndpoint(s *Session, page *dockerResourcesPage, network monitor.DockerNetworkDetails, disconnect bool) {
	title := locales.DockerResConnect()
	if disconnect {
		title = locales.DockerResDisconnect()
	}
	var picked, ipv4, aliases string
	var force bool
	page.show(title, func(c *ui.Context, dialog *dockerResourceDialog, open *bool) {
		current, found := lookupNetwork(s, page, network.ID)
		choices := networkEndpointChoices(s, current, disconnect)
		ids := make(map[string]string)
		var names []string
		for _, item := range choices {
			name := cmp.Or(item.Name, item.ID)
			if _, exists := ids[name]; exists {
				name += " · " + item.ID
			}
			ids[name] = item.ID
			names = append(names, name)
		}
		if !slices.Contains(names, picked) {
			picked = ""
		}
		if picked == "" && len(names) > 0 {
			picked = names[0]
		}
		ui.Text(c, network.Name).Bold().Selectable()
		ui.Text(c, locales.DockerContainers()).FontSize(12)
		ui.Select(c, &picked, names).Label(locales.DockerContainers()).Disabled(len(names) == 0).FillWidth()
		var script string
		var err error
		if disconnect {
			ui.Text(c, locales.DockerResDisconnectWarning()).TextColor(theme.Danger)
			ui.Checkbox(c, &force, locales.DockerResForceDisconnect()).Disabled(page.tool.Tool == "podman").Tooltip(locales.DockerResPodman())
			script, err = page.tool.NetworkDisconnect(network.ID, ids[picked], force)
		} else {
			resourceText(c, locales.DockerResIPv4(), &ipv4)
			resourceText(c, locales.DockerResAliases(), &aliases)
			script, err = page.tool.NetworkConnect(network.ID, ids[picked], strings.TrimSpace(ipv4), aliases)
			if address, parseErr := netip.ParseAddr(strings.TrimSpace(ipv4)); parseErr == nil && !slices.ContainsFunc(current.Subnets, func(subnet monitor.DockerSubnet) bool {
				prefix, _ := netip.ParsePrefix(subnet.Subnet)
				return prefix.Contains(address)
			}) {
				err = monitor.ErrDockerResourceInput
			}
		}
		reason := cmp.Or(page.blocked(s), resourceListReason(&page.networks.dockerResourceLoad))
		connectReason, disconnectReason := networkEndpointReasons(s, current, reason)
		reason = connectReason
		if disconnect {
			reason = disconnectReason
		}
		if !found {
			reason = locales.DockerResStale()
		}
		if err != nil {
			reason = cmp.Or(reason, locales.DockerResBadEndpoint())
		}
		if reason != "" {
			ui.Text(c, reason).TextColor(theme.Danger)
		}
		if resourceButtons(c, open, title, reason) {
			a.runDockerResource(s, page, dialog, dockerResourceStep{script: script})
		}
	})
}

func (a *App) confirmNetworkRemove(s *Session, page *dockerResourcesPage, networks []monitor.DockerNetworkDetails, prune bool) {
	title := locales.DockerResNetRemove()
	if prune {
		title = locales.DockerResNetPrune()
	}
	networks = slices.Clone(networks)
	slices.SortFunc(networks, func(left, right monitor.DockerNetworkDetails) int { return strings.Compare(left.Name, right.Name) })
	page.show(title, func(c *ui.Context, dialog *dockerResourceDialog, open *bool) {
		ui.Text(c, locales.DockerResNetworkWarning()).TextColor(theme.Danger)
		ui.Text(c, locales.DockerResCount(len(networks))).Bold()
		var latest []monitor.DockerNetworkDetails
		reason := cmp.Or(page.blocked(s), resourceListReason(&page.networks.dockerResourceLoad))
		for _, network := range networks {
			ui.Text(c.Key(network.ID), network.Name).Selectable()
			item, ok := lookupNetwork(s, page, network.ID)
			if !ok || item.Name != network.Name {
				reason = cmp.Or(reason, locales.DockerResStale())
				continue
			}
			reason = cmp.Or(reason, networkRemoveReason(item))
			latest = append(latest, item)
		}
		script, err := page.tool.NetworkRemove(latest...)
		if err != nil {
			reason = cmp.Or(reason, locales.DockerResNoCandidates())
		}
		if resourceButtons(c, open, title, reason) {
			a.runDockerResource(s, page, dialog, dockerResourceStep{script: script})
		}
	})
}
