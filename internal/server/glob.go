package server

import (
	"fmt"
	"regexp"
	"strings"
)

// globFilter is the grep fallback's reading of rg -g, so a host without
// ripgrep honours the same include and exclude field.
//
// The rules follow rg's, which follow .gitignore's: a glob with no slash
// matches any one path component — so `*.go` matches a file name at any depth
// and `!node_modules` excludes that folder wherever it is — and a glob with a
// slash matches from the root. With any include present, a path must match at
// least one; any exclude that matches drops it.
type globFilter struct {
	include []globPattern
	exclude []globPattern
}

type globPattern struct {
	re       *regexp.Regexp
	anchored bool
}

func compileGlobs(globs []string) (*globFilter, error) {
	if len(globs) == 0 {
		return nil, nil
	}
	f := &globFilter{}
	for _, g := range globs {
		negate := strings.HasPrefix(g, "!")
		g = strings.TrimPrefix(g, "!")
		anchored := strings.Contains(strings.TrimSuffix(g, "/"), "/")
		re, err := regexp.Compile("^" + globRegexp(strings.Trim(g, "/")) + "$")
		if err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", g, err)
		}
		p := globPattern{re: re, anchored: anchored}
		if negate {
			f.exclude = append(f.exclude, p)
		} else {
			f.include = append(f.include, p)
		}
	}
	return f, nil
}

// allows reports whether a root-relative, slash-separated path passes.
func (f *globFilter) allows(rel string) bool {
	if f == nil {
		return true
	}
	for _, p := range f.exclude {
		if p.matches(rel) {
			return false
		}
	}
	if len(f.include) == 0 {
		return true
	}
	for _, p := range f.include {
		if p.matches(rel) {
			return true
		}
	}
	return false
}

func (p globPattern) matches(rel string) bool {
	if p.anchored {
		// A glob naming a folder takes everything under it.
		for prefix := rel; prefix != "."; {
			if p.re.MatchString(prefix) {
				return true
			}
			cut := strings.LastIndexByte(prefix, '/')
			if cut < 0 {
				break
			}
			prefix = prefix[:cut]
		}
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if p.re.MatchString(part) {
			return true
		}
	}
	return false
}

// globRegexp translates one glob: ** spans folders, * and ? stay inside one,
// {a,b} is a choice, and [...] passes through as a class.
func globRegexp(glob string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && strings.HasPrefix(glob[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case c == '*' && strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '{':
			b.WriteString("(?:")
			depth++
		case c == '}' && depth > 0:
			b.WriteString(")")
			depth--
		case c == ',' && depth > 0:
			b.WriteString("|")
		case c == '[':
			end := strings.IndexByte(glob[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := glob[i+1 : i+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + class + "]")
			i += end + 1
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	for ; depth > 0; depth-- {
		b.WriteString(")")
	}
	return b.String()
}
