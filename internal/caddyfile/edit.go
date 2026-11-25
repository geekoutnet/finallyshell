package caddyfile

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

const (
	disabledBegin = "# finallyshell: disabled site"
	disabledLine  = "#| "
	disabledEnd   = "# finallyshell: end disabled site"
)

func (doc *Document) Text(node *Node) string {
	if node == nil || node.Start < 0 || node.End < node.Start || node.End > len(doc.Source) {
		return ""
	}
	return doc.Source[node.Start:node.End]
}

func (doc *Document) SiteText(node *Node) string {
	if node != nil && node.Kind == Disabled {
		return node.disabledText
	}
	return doc.Text(node)
}

func (doc *Document) HasComments(node *Node) bool {
	if node == nil {
		return false
	}
	index, _ := slices.BinarySearchFunc(doc.Comments, node.Start, func(comment Token, pos int) int { return comment.Start - pos })
	return index < len(doc.Comments) && doc.Comments[index].Start < node.End
}

func (doc *Document) Replace(node *Node, replacement string) (string, error) {
	if node == nil || !hasNode(doc.Nodes, node) || doc.Text(node) == "" {
		return "", fmt.Errorf("node does not belong to the Caddyfile")
	}
	return doc.Source[:node.Start] + replacement + doc.Source[node.End:], nil
}

func (doc *Document) ReplaceSite(node *Node, text string) (string, error) {
	if node == nil || node.Kind != Site || !slices.Contains(doc.Nodes, node) {
		return "", fmt.Errorf("site does not belong to the Caddyfile")
	}
	text = strings.TrimSpace(text)
	replacement, err := parse(text, false)
	if err != nil || len(replacement.Nodes) != 1 || replacement.Nodes[0].Kind != Site || !replacement.Nodes[0].Block || replacement.Nodes[0].Start != 0 || replacement.Nodes[0].End != len(text) {
		return "", fmt.Errorf("expected one complete site block")
	}
	return doc.Replace(node, styleText(text, "", doc.eol()))
}

func hasNode(nodes []*Node, target *Node) bool {
	for _, node := range nodes {
		if node == target || node.Kind != Disabled && hasNode(node.Children, target) {
			return true
		}
	}
	return false
}

func (doc *Document) Append(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return doc.Source, nil
	}
	eol := doc.eol()
	original := doc.Source
	for _, site := range doc.Sites() {
		if site.Kind == Site && !site.Block {
			wrapped := original[:site.HeadEnd] + " {" + original[site.HeadEnd:site.End] + eol + "}" + original[site.End:]
			original = wrapped
			break
		}
	}
	if original != "" && original != "\ufeff" {
		if !strings.HasSuffix(original, "\n") {
			original += eol
		}
		if !strings.HasSuffix(original, eol+eol) {
			original += eol
		}
	}
	return original + styleText(text, "", eol) + eol, nil
}

func (doc *Document) RenameSite(site *Node, addresses []string) (string, error) {
	if site == nil || site.Kind != Site || !slices.Contains(doc.Nodes, site) || len(addresses) == 0 {
		return "", fmt.Errorf("invalid site or addresses")
	}
	for _, address := range addresses {
		tokens, err := Lex(address)
		if err != nil || len(tokens) != 1 || tokens[0].Kind != Word || tokens[0].Quoted || tokens[0].Value != address || strings.Contains(address, ",") {
			return "", fmt.Errorf("invalid site address")
		}
	}
	return doc.Source[:site.Start] + strings.Join(addresses, ", ") + doc.Source[site.HeadEnd:], nil
}

func (doc *Document) GlobalOptions(name string) []*Node {
	var options []*Node
	if block := doc.GlobalBlock(); block != nil {
		for _, child := range block.Children {
			if child.Name == name {
				options = append(options, child)
			}
		}
	}
	return options
}

func (doc *Document) SetGlobal(name, text string) (string, error) {
	text = strings.TrimSpace(text)
	eol := doc.eol()
	if text != "" {
		wrapped := "{\n" + text + "\n}"
		candidate, err := parse(wrapped, false)
		if err != nil || len(candidate.Nodes) != 1 || candidate.GlobalBlock() == nil || len(candidate.Nodes[0].Children) != 1 || candidate.Nodes[0].Children[0].Name != name || candidate.Nodes[0].Children[0].Kind != Directive {
			return "", fmt.Errorf("expected exactly one %s option", name)
		}
	}
	existing := doc.GlobalOptions(name)
	if len(existing) > 1 {
		return "", fmt.Errorf("more than one %s option", name)
	}
	if len(existing) == 1 {
		return doc.Replace(existing[0], styleText(text, "", eol))
	}
	if text == "" {
		return doc.Source, nil
	}
	if global := doc.GlobalBlock(); global != nil {
		position := doc.lineStart(global.Close)
		prefix := ""
		if strings.TrimSpace(doc.Source[position:global.Close]) != "" {
			position, prefix = global.Close, eol
		}
		return doc.Source[:position] + prefix + styleText(text, "\t", eol) + eol + doc.Source[position:], nil
	}
	position := len(doc.Source)
	if len(doc.Nodes) != 0 {
		position = doc.lineStart(doc.Nodes[0].Start)
	}
	if position == 0 && strings.HasPrefix(doc.Source, "\ufeff") {
		position = len("\ufeff")
	}
	prefix := ""
	if position > len("\ufeff") || position > 0 && !strings.HasPrefix(doc.Source, "\ufeff") {
		if doc.Source[position-1] != '\n' {
			prefix = eol
		}
	}
	return doc.Source[:position] + prefix + "{" + eol + styleText(text, "\t", eol) + eol + "}" + eol + eol + doc.Source[position:], nil
}

func styleText(text, indent, eol string) string {
	if text == "" || indent == "" && eol == "\n" {
		return text
	}
	tokens, err := Lex(text)
	if err != nil {
		return indent + text
	}
	var formatted strings.Builder
	formatted.WriteString(indent)
	start := 0
	for _, token := range tokens {
		if token.Kind != Newline {
			continue
		}
		formatted.WriteString(text[start:token.Start])
		formatted.WriteString(eol)
		formatted.WriteString(indent)
		start = token.End
	}
	formatted.WriteString(text[start:])
	return formatted.String()
}

func (doc *Document) eol() string {
	first := strings.IndexByte(doc.Source, '\n')
	if first > 0 && doc.Source[first-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

func (doc *Document) lineStart(pos int) int {
	return strings.LastIndexByte(doc.Source[:pos], '\n') + 1
}

func (doc *Document) DisableSite(site *Node) (string, error) {
	if site == nil || site.Kind != Site || !slices.Contains(doc.Nodes, site) || strings.TrimSpace(strings.TrimPrefix(doc.Source[doc.lineStart(site.Start):site.Start], "\ufeff")) != "" {
		return "", fmt.Errorf("site cannot be commented out here")
	}
	suffix, _, _ := strings.Cut(doc.Source[site.End:], "\n")
	if rest := strings.TrimSpace(suffix); rest != "" && !strings.HasPrefix(rest, "#") {
		return "", fmt.Errorf("site shares its last line with other configuration")
	}
	text, eol := doc.Text(site), doc.eol()
	return doc.Replace(site, disabledBegin+eol+disabledLine+strings.ReplaceAll(text, "\n", "\n"+disabledLine)+eol+disabledEnd)
}

func (doc *Document) EnableSite(site *Node) (string, error) {
	if site == nil || site.Kind != Disabled || !slices.Contains(doc.Nodes, site) || site.disabledText == "" {
		return "", fmt.Errorf("site is not disabled")
	}
	return doc.Replace(site, site.disabledText)
}

func (doc *Document) readDisabled() {
	active := doc.Nodes
	activeIndex := 0
	for index, token := range doc.Comments {
		if token.Value != disabledBegin || strings.TrimSpace(strings.TrimPrefix(doc.Source[doc.lineStart(token.Start):token.Start], "\ufeff")) != "" {
			continue
		}
		for activeIndex < len(active) && active[activeIndex].End <= token.Start {
			activeIndex++
		}
		var body strings.Builder
		end, previous := 0, token
		for _, next := range doc.Comments[index+1:] {
			if next.Line != previous.Line+1 || doc.lineStart(next.Start) != next.Start {
				break
			}
			if rest, matches := strings.CutPrefix(next.Value, disabledEnd); matches {
				if rest = strings.TrimSpace(rest); rest == "" || strings.HasPrefix(rest, "#") {
					end = next.Start + len(disabledEnd)
				}
				break
			}
			if !strings.HasPrefix(next.Value, disabledLine) {
				break
			}
			if body.Len() > 0 {
				body.WriteString(doc.Source[previous.End:next.Start])
			}
			body.WriteString(strings.TrimPrefix(next.Value, disabledLine))
			previous = next
		}
		if end == 0 || activeIndex < len(active) && active[activeIndex].Start < end || strings.TrimSpace(body.String()) == "" {
			continue
		}
		parsed, err := parse(body.String(), false)
		if err != nil || len(parsed.Nodes) != 1 || parsed.Nodes[0].Kind != Site {
			continue
		}
		node := *parsed.Nodes[0]
		node.Kind, node.Start, node.End, node.Line = Disabled, token.Start, end, token.Line
		node.Open, node.Close, node.HeadEnd = -1, -1, token.End
		node.disabledText = body.String()
		doc.Nodes = append(doc.Nodes, &node)
	}
	slices.SortStableFunc(doc.Nodes, func(left, right *Node) int { return left.Start - right.Start })
}

func AddressList(text string) ([]string, error) {
	tokens, err := Lex(strings.TrimSpace(text))
	if err != nil {
		return nil, err
	}
	var entries []Token
	for _, token := range tokens {
		if token.Kind == Newline {
			continue
		}
		if token.Kind != Word || token.Quoted || strings.IndexFunc(token.Value, unicode.IsSpace) != -1 {
			return nil, fmt.Errorf("invalid site address")
		}
		entries = append(entries, token)
	}
	return splitAddresses(entries), nil
}
