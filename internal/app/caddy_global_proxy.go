package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
)

func caddyGlobalProxyPreset(text string, ranges []string) (string, error) {
	if len(ranges) == 0 {
		return "", caddyGlobalInvalid
	}
	for _, value := range ranges {
		if value == "private_ranges" {
			continue
		}
		if _, err := netip.ParsePrefix(value); err != nil {
			return "", caddyGlobalInvalid
		}
	}
	directive := "trusted_proxies static " + strings.Join(ranges, " ")
	_, node, err := caddyGlobalOptionNode(text, "servers")
	if err != nil || !node.Block {
		return "", caddyGlobalInvalid
	}
	var start, end int
	for _, child := range node.Children {
		if child.Name != "trusted_proxies" {
			continue
		}
		if end != 0 {
			return "", caddyGlobalInvalid
		}
		start, end = child.Start-2, child.End-2
	}
	if end != 0 {
		return text[:start] + directive + text[end:], nil
	}
	return caddyGlobalInsertChild(text, node.Close-2, directive), nil
}

func caddyGlobalInsertChild(text string, close int, directive string) string {
	line := strings.LastIndex(text[:close], "\n") + 1
	newline := caddyGlobalEOL(text)
	if indent := text[line:close]; strings.Trim(indent, " \t") == "" {
		childIndent := indent + "\t"
		styled := caddyGlobalStyle(directive, childIndent, newline)
		return text[:line] + childIndent + styled + newline + text[line:]
	}
	return text[:close] + newline + "\t" + directive + newline + text[close:]
}

func caddyGlobalCloudflareRanges(ctx context.Context, client *http.Client) ([]string, error) {
	const limit = 32768
	var ranges []string
	seen := map[netip.Prefix]bool{}
	for _, family := range []string{"4", "6"} {
		address := "https://www.cloudflare.com/ips-v" + family
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK || len(body) > limit {
			return nil, fmt.Errorf("invalid Cloudflare IPv%s range response", family)
		}
		before := len(ranges)
		for _, value := range strings.Fields(string(body)) {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || prefix.Addr().Is4() != (family == "4") || prefix.Addr().Is4In6() || prefix.Masked() != prefix || prefix.Bits() == 0 {
				return nil, fmt.Errorf("invalid Cloudflare IPv%s network", family)
			}
			if !seen[prefix] {
				seen[prefix] = true
				ranges = append(ranges, prefix.String())
			}
		}
		if len(ranges) == before {
			return nil, fmt.Errorf("empty Cloudflare IPv%s ranges", family)
		}
	}
	return ranges, nil
}
