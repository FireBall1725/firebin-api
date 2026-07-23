// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package kicad parses KiCad design sources (schematic s-expressions and BOM
// CSV exports) into a normalized bill of materials.
package kicad

import (
	"errors"
	"strings"
	"unicode"
)

// node is one s-expression element: either an atom (Value set, Children nil) or
// a list (Children set). For a list, Children[0] is conventionally the head atom.
type node struct {
	Value    string
	Children []*node
	IsList   bool
}

// head returns the first child's atom value ("symbol", "property", …) or "".
func (n *node) head() string {
	if n == nil || !n.IsList || len(n.Children) == 0 {
		return ""
	}
	return n.Children[0].Value
}

// atom returns the atom value of the i-th child, or "" if out of range / a list.
func (n *node) atom(i int) string {
	if n == nil || i < 0 || i >= len(n.Children) {
		return ""
	}
	c := n.Children[i]
	if c.IsList {
		return ""
	}
	return c.Value
}

// parseSexpr parses a full s-expression document into its root list.
func parseSexpr(data []byte) (*node, error) {
	p := &sexprParser{s: string(data)}
	p.skipSpace()
	if p.pos >= len(p.s) || p.s[p.pos] != '(' {
		return nil, errors.New("not an s-expression (expected '(')")
	}
	root, err := p.parseList()
	if err != nil {
		return nil, err
	}
	return root, nil
}

type sexprParser struct {
	s   string
	pos int
}

func (p *sexprParser) skipSpace() {
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			p.pos++
			continue
		}
		break
	}
}

// parseList assumes the current char is '(' and consumes the matching ')'.
func (p *sexprParser) parseList() (*node, error) {
	p.pos++ // consume '('
	list := &node{IsList: true}
	for {
		p.skipSpace()
		if p.pos >= len(p.s) {
			return nil, errors.New("unexpected EOF in list")
		}
		switch p.s[p.pos] {
		case ')':
			p.pos++
			return list, nil
		case '(':
			child, err := p.parseList()
			if err != nil {
				return nil, err
			}
			list.Children = append(list.Children, child)
		case '"':
			list.Children = append(list.Children, &node{Value: p.parseQuoted()})
		default:
			list.Children = append(list.Children, &node{Value: p.parseAtom()})
		}
	}
}

// parseQuoted reads a "…" token, honoring KiCad's backslash escapes.
func (p *sexprParser) parseQuoted() string {
	p.pos++ // consume opening quote
	var b strings.Builder
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if c == '\\' && p.pos+1 < len(p.s) {
			n := p.s[p.pos+1]
			switch n {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte(n)
			}
			p.pos += 2
			continue
		}
		if c == '"' {
			p.pos++
			break
		}
		b.WriteByte(c)
		p.pos++
	}
	return b.String()
}

// parseAtom reads a bare token up to the next whitespace or bracket.
func (p *sexprParser) parseAtom() string {
	start := p.pos
	for p.pos < len(p.s) {
		c := rune(p.s[p.pos])
		if unicode.IsSpace(c) || c == '(' || c == ')' {
			break
		}
		p.pos++
	}
	return p.s[start:p.pos]
}
