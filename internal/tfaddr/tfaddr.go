// Package tfaddr parses Terraform resource addresses such as
// `module.web["eu"].aws_instance.app[0]` without a full HCL dependency.
package tfaddr

import "strings"

// Segments splits an address on dots that are outside instance keys and
// drops the instance keys themselves:
// `module.web["a.b"].aws_instance.x[0]` -> [module web aws_instance x].
// Runs in O(n) for an address of n bytes.
func Segments(addr string) []string {
	var segs []string
	var cur strings.Builder
	walk(addr, func(c byte, inKey bool) {
		switch {
		case inKey:
		case c == '.':
			segs = append(segs, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	})
	return append(segs, cur.String())
}

// StripKeys removes instance keys:
// `module.web[0].aws_instance.web["a"]` -> `module.web.aws_instance.web`.
func StripKeys(addr string) string {
	var b strings.Builder
	walk(addr, func(c byte, inKey bool) {
		if !inKey {
			b.WriteByte(c)
		}
	})
	return b.String()
}

// walk calls fn for every byte, reporting whether it is part of an instance
// key ("[...]", brackets included). Quoted keys may contain "]", "." and
// escaped quotes.
func walk(addr string, fn func(c byte, inKey bool)) {
	depth, inString := 0, false
	for i := 0; i < len(addr); i++ {
		c := addr[i]
		if inString {
			fn(c, true)
			switch c {
			case '\\':
				if i+1 < len(addr) {
					i++
					fn(addr[i], true)
				}
			case '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '[':
			depth++
			fn(c, true)
		case c == ']' && depth > 0:
			depth--
			fn(c, true)
		case c == '"' && depth > 0:
			inString = true
			fn(c, true)
		default:
			fn(c, depth > 0)
		}
	}
}

// KeyEnd returns the index just past a leading instance key in s (for
// example 3 for `[0].x`), or -1 if s does not start with a complete key.
func KeyEnd(s string) int {
	if !strings.HasPrefix(s, "[") {
		return -1
	}
	depth, inString := 0, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}
