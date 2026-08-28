// Copyright 2026 The Phase Contributors
// SPDX-License-Identifier: MIT

package phase

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Suites: a suite is a NAMED SELECTION, and selection
// is a tag expression — never an engine type. One case belongs to many
// suites by carrying many tags; the Runner, Config, Preflight and the
// report know nothing about any of it. The report describes execution;
// which suite asked for it is the selecting layer's provenance to log.

// Tagged is the optional interface a Case implements to carry suite tags.
// A case without it simply has no tags (it still matches pure negations).
type Tagged interface {
	Tags() []string
}

// ErrNoMatch: a selector matching zero cases is a refusal — running nothing
// and reporting green is the founding defect wearing a suite's clothes.
// Typed, so CI wrappers branch on errors.Is, never on message strings.
var ErrNoMatch = errors.New("phase: selector matched no cases")

// SelectByTags filters cases by a boolean tag expression — `&&`, `||`, `!`
// and parentheses over exact tag names (no hierarchy, no wildcards, no
// regex: undemonstrated power is unpaid debt). Declaration order is
// preserved. A malformed expression is an error naming the position; a
// well-formed expression matching nothing returns ErrNoMatch.
//
//	smoke, err := phase.SelectByTags(cases, "smoke && !slow")
//
// "Run this one case regardless of tags" is plain slice filtering — not a
// tag feature.
func SelectByTags(cases []Case, expr string) ([]Case, error) {
	root, err := parseTagExpr(expr)
	if err != nil {
		return nil, err
	}
	var out []Case
	for _, c := range cases {
		tags := map[string]bool{}
		if tc, ok := c.(Tagged); ok {
			for _, tag := range tc.Tags() {
				tags[tag] = true
			}
		}
		if root.eval(tags) {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w (expression %q over %d case(s))", ErrNoMatch, expr, len(cases))
	}
	return out, nil
}

// --- a small recursive-descent parser: or := and ('||' and)*;
// and := unary ('&&' unary)*; unary := '!' unary | '(' or ')' | tag ---

type tagExpr interface{ eval(map[string]bool) bool }

type tagLeaf string

func (t tagLeaf) eval(tags map[string]bool) bool { return tags[string(t)] }

type tagNot struct{ x tagExpr }

func (n tagNot) eval(tags map[string]bool) bool { return !n.x.eval(tags) }

type tagBin struct {
	and  bool
	l, r tagExpr
}

func (b tagBin) eval(tags map[string]bool) bool {
	if b.and {
		return b.l.eval(tags) && b.r.eval(tags)
	}
	return b.l.eval(tags) || b.r.eval(tags)
}

type tagParser struct {
	in  string
	pos int
}

func parseTagExpr(expr string) (tagExpr, error) {
	p := &tagParser{in: expr}
	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.in) {
		return nil, fmt.Errorf("phase: tag expression %q: unexpected %q at position %d", expr, p.in[p.pos:], p.pos)
	}
	return root, nil
}

func (p *tagParser) parseOr() (tagExpr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.consume("||") {
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = tagBin{and: false, l: l, r: r}
	}
	return l, nil
}

func (p *tagParser) parseAnd() (tagExpr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.consume("&&") {
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = tagBin{and: true, l: l, r: r}
	}
	return l, nil
}

func (p *tagParser) parseUnary() (tagExpr, error) {
	p.skipSpace()
	switch {
	case p.consume("!"):
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return tagNot{x: x}, nil
	case p.consume("("):
		x, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.consume(")") {
			return nil, fmt.Errorf("phase: tag expression %q: missing ')' at position %d", p.in, p.pos)
		}
		return x, nil
	default:
		start := p.pos
		for p.pos < len(p.in) && isTagRune(rune(p.in[p.pos])) {
			p.pos++
		}
		if p.pos == start {
			return nil, fmt.Errorf("phase: tag expression %q: expected a tag at position %d", p.in, start)
		}
		return tagLeaf(p.in[start:p.pos]), nil
	}
}

func (p *tagParser) skipSpace() {
	for p.pos < len(p.in) && unicode.IsSpace(rune(p.in[p.pos])) {
		p.pos++
	}
}

func (p *tagParser) consume(tok string) bool {
	p.skipSpace()
	if strings.HasPrefix(p.in[p.pos:], tok) {
		p.pos += len(tok)
		return true
	}
	return false
}

func isTagRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.'
}
