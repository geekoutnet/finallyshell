package caddyfile

import (
	"fmt"
	"slices"
	"strings"
)

type Kind string

const (
	Site       Kind = "site"
	Global     Kind = "global"
	Snippet    Kind = "snippet"
	NamedRoute Kind = "named route"
	Import     Kind = "import"
	Directive  Kind = "directive"
	Matcher    Kind = "matcher"
	Disabled   Kind = "disabled site"
)

type Node struct {
	Kind                      Kind
	Name                      string
	Args                      []string
	Children                  []*Node
	Block                     bool
	Start, End, Line, HeadEnd int
	Open, Close               int
	disabledText              string
}

type Document struct {
	Source   string
	Nodes    []*Node
	Comments []Token
}

type parser struct {
	tokens []Token
	pos    int
	depth  int
	doc    *Document
}

func Parse(source string) (*Document, error) {
	return parse(source, true)
}

func parse(source string, disabled bool) (*Document, error) {
	tokens, err := Lex(source)
	if err != nil {
		return nil, err
	}
	doc := &Document{Source: source}
	for _, token := range tokens {
		if token.Kind == Comment {
			doc.Comments = append(doc.Comments, token)
		}
	}
	state := parser{tokens: tokens, doc: doc}
	doc.Nodes, err = state.sequence(true, false)
	if err != nil {
		return nil, err
	}
	if disabled {
		doc.readDisabled()
	}
	return doc, nil
}

func (state *parser) peek() Token {
	if state.pos == len(state.tokens) {
		return Token{Start: len(state.doc.Source), End: len(state.doc.Source)}
	}
	return state.tokens[state.pos]
}

func (state *parser) separators() {
	for state.pos < len(state.tokens) {
		kind := state.peek().Kind
		if kind != Newline && kind != Comment {
			return
		}
		state.pos++
	}
}

func (state *parser) sequence(top, brace bool) ([]*Node, error) {
	if state.depth >= 256 {
		return nil, fmt.Errorf("Caddyfile nesting exceeds 256 blocks")
	}
	state.depth++
	defer func() { state.depth-- }()
	var nodes []*Node
	for {
		state.separators()
		first := state.peek()
		switch first.Kind {
		case "":
			if brace {
				return nil, fmt.Errorf("Caddyfile: missing closing brace")
			}
			return nodes, nil
		case CloseBrace:
			if brace {
				return nodes, nil
			}
			return nil, fmt.Errorf("Caddyfile line %d: unexpected closing brace", first.Line)
		}
		node, err := state.statement(top, len(nodes) == 0)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
}

func (state *parser) statement(top, firstStatement bool) (*Node, error) {
	first := state.peek()
	node := &Node{Start: first.Start, End: first.End, Line: first.Line, Open: -1, Close: -1}
	var header []Token
	for {
		next := state.peek()
		if next.Kind == Word {
			header = append(header, next)
			node.End = next.End
			state.pos++
			continue
		}
		if top && len(header) > 0 && !header[0].Quoted && header[0].Value != "import" && (next.Kind == Newline || next.Kind == Comment) && strings.HasSuffix(header[len(header)-1].Value, ",") {
			state.separators()
			continue
		}
		break
	}
	node.HeadEnd = node.End
	if len(header) == 0 {
		if !top || !firstStatement || first.Kind != OpenBrace {
			return nil, fmt.Errorf("Caddyfile line %d: unexpected opening brace", first.Line)
		}
		node.Kind, node.HeadEnd = Global, node.Start
	} else {
		node.Name = first.Value
		for _, token := range header[1:] {
			node.Args = append(node.Args, token.Value)
		}
		switch {
		case !first.Quoted && first.Value == "import":
			node.Kind = Import
		case top && len(header) == 1 && !first.Quoted && wrapped(first.Value, "&("):
			node.Kind, node.Name = NamedRoute, first.Value[2:len(first.Value)-1]
		case top && len(header) == 1 && !first.Quoted && wrapped(first.Value, "("):
			node.Kind, node.Name = Snippet, first.Value[1:len(first.Value)-1]
		case top:
			node.Kind = Site
			node.Args = splitAddresses(header)
			if len(node.Args) == 0 {
				return nil, fmt.Errorf("Caddyfile line %d: missing site addresses", first.Line)
			}
			node.Name = node.Args[0]
		case !first.Quoted && strings.HasPrefix(first.Value, "@"):
			node.Kind = Matcher
		default:
			node.Kind = Directive
		}
	}
	if open := state.peek(); open.Kind == OpenBrace {
		node.Block, node.Open = true, open.Start
		state.pos++
		children, err := state.sequence(false, true)
		if err != nil {
			return nil, err
		}
		close := state.peek()
		node.Children, node.End, node.Close = children, close.End, close.Start
		state.pos++
		if top && (state.peek().Kind == Word || state.peek().Kind == OpenBrace) {
			return nil, fmt.Errorf("Caddyfile line %d: expected a new line after the block", close.Line)
		}
	} else if top && node.Kind == Site {
		children, err := state.sequence(false, false)
		if err != nil {
			return nil, err
		}
		node.Children = children
		if len(children) > 0 {
			node.End = children[len(children)-1].End
		}
	} else if node.Kind == Snippet || node.Kind == NamedRoute {
		return nil, fmt.Errorf("Caddyfile line %d: %s needs a block", first.Line, node.Kind)
	}
	return node, nil
}

func wrapped(text, prefix string) bool {
	return len(text) > len(prefix)+1 && strings.HasPrefix(text, prefix) && strings.HasSuffix(text, ")")
}

func splitAddresses(tokens []Token) []string {
	var addresses []string
	for _, token := range tokens {
		if token.Quoted || strings.Contains(token.Value, "{") {
			if address := strings.TrimSuffix(token.Value, ","); address != "" {
				addresses = append(addresses, address)
			}
			continue
		}
		for address := range strings.SplitSeq(token.Value, ",") {
			if address != "" {
				addresses = append(addresses, address)
			}
		}
	}
	return addresses
}

func (doc *Document) Sites() []*Node {
	return doc.byKind(Site, Disabled)
}

func (doc *Document) Snippets() []*Node {
	return doc.byKind(Snippet)
}

func (doc *Document) GlobalBlock() *Node {
	for _, node := range doc.Nodes {
		if node.Kind == Disabled {
			continue
		}
		if node.Kind == Global {
			return node
		}
		break
	}
	return nil
}

func (doc *Document) byKind(kinds ...Kind) []*Node {
	var nodes []*Node
	for _, node := range doc.Nodes {
		if slices.Contains(kinds, node.Kind) {
			nodes = append(nodes, node)
		}
	}
	return nodes
}
