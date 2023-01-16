// Package pathutil contains additional functions for handling paths that aren't
// in the standard library but probably ought to be. Most of them are based on
// functions in cmd/go.
//
// These functions work on both slash-separated paths and file system paths that
// use the OS-specific separator (`\` on Windows).
//
// These functions generally treat each path as a list of components and do not
// allow partial matches of a component. For example,
// HasPathPrefix("aa/bb", "a") returns false since the path argument starts
// with "aa", not "a". strings.HasPathPrefix would return true unless you
// use "a/" as the prefix, but that would break if the path only has
// one component.
package pathutil

import (
	"fmt"
	"path/filepath"
	"strings"
)

// HasPathPrefix reports whether the slash-separated path s
// begins with the elements in prefix.
// Based on cmd/go/internal/str.HasPathPrefix.
func HasPathPrefix(s, prefix string) bool {
	if len(s) == len(prefix) {
		return s == prefix
	}
	if prefix == "" {
		return true
	}
	if len(s) > len(prefix) {
		if prefix[len(prefix)-1] == '/' || s[len(prefix)] == '/' {
			return s[:len(prefix)] == prefix
		}
	}
	return false
}

// HasFilePathPrefix reports whether the filesystem path s
// begins with the elements in prefix.
// Based on cmd/go/internal/str.HasFilePathPrefix.
func HasFilePathPrefix(s, prefix string) bool {
	sv := strings.ToUpper(filepath.VolumeName(s))
	pv := strings.ToUpper(filepath.VolumeName(prefix))
	s = s[len(sv):]
	prefix = prefix[len(pv):]
	switch {
	default:
		return false
	case pv != "" && sv != pv:
		return false
	case len(s) == len(prefix):
		return s == prefix
	case prefix == "":
		return true
	case len(s) > len(prefix):
		if prefix[len(prefix)-1] == filepath.Separator {
			return strings.HasPrefix(s, prefix)
		}
		return s[len(prefix)] == filepath.Separator && s[:len(prefix)] == prefix
	}
}

// TrimFilePathPrefix returns the given filesystem path s without the leading
// prefix.
func TrimFilePathPrefix(s, prefix string) string {
	sv := strings.ToUpper(filepath.VolumeName(s))
	pv := strings.ToUpper(filepath.VolumeName(prefix))
	s = s[len(sv):]
	prefix = prefix[len(pv):]

	if !HasFilePathPrefix(s, prefix) || len(prefix) == 0 {
		return s
	}
	if len(s) == len(prefix) {
		return ""
	}
	if prefix[len(prefix)-1] == filepath.Separator {
		return strings.TrimPrefix(s, prefix)
	}
	return s[len(prefix)+1:]
}

// TrimPathPrefix returns p without the leading prefix. Unlike
// strings.TrimPrefix, the prefix will only match on slash-separted component
// boundaries, so TrimPathPrefix("aa/b", "aa") returns "b", but
// TrimPathPrefix("aa/b", "a") returns "aa/b".
func TrimPathPrefix(p, prefix string) string {
	if prefix == "" {
		return p
	}
	if prefix == p {
		return ""
	}
	return strings.TrimPrefix(p, prefix+"/")
}

// PathLen returns the number of path components in the string. In most cases,
// this is the number of '/' characters plus one, though '/' at the beginning
// or ending of the string is not counted. PathLen returns 0 for the empty
// string and for "/".
func PathLen(s string) int {
	if s == "" || s == "/" {
		return 0
	}
	if s[0] == '/' {
		s = s[1:]
	}
	if s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return strings.Count(s, "/") + 1
}

// PathComponent returns the ith component of the slash-separated path in s.
// s must have at least i+1 components, according to PathLen.
func PathComponent(s string, i int) string {
	// Trim leading and trailing slashes.
	orig := s
	if len(s) > 0 && s[0] == '/' {
		s = s[1:]
	}
	if len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}

	// Loop over path components.
	begin := 0
	j := 0
	for begin < len(s) {
		sep := strings.IndexByte(s[begin:], '/')
		var end int
		if sep < 0 {
			end = len(s)
		} else {
			end = begin + sep
		}

		if i == j {
			return s[begin:end]
		}
		j++
		begin = end + 1
	}
	if i == j && s[len(s)-1] == '/' {
		// Special case: the string ended with two trailing slashes, and we want
		// the empty component between them, for example, PathComponent("a//", 1).
		return ""
	}

	panic(fmt.Sprintf("path %q has no component at index %d", orig, i))
}

// QuoteForShell quotes path if it contains any characters that would be
// interpreted by a shell.
func QuoteForShell(path string) string {
	const specialChars = "~`#$^&*()\\|[]{};'\"<>?! \t\r\n"
	if path != "" && !strings.ContainsAny(path, specialChars) {
		return path
	}
	b := &strings.Builder{}
	b.WriteByte('"')
	quoteReplacer.WriteString(b, path)
	b.WriteByte('"')
	return b.String()
}

var quoteReplacer = strings.NewReplacer(`"`, `\"`, `\`, `\\`)

// SplitUnquote splits a string into tokens separated by spaces or tabs.
//
// A token may use single or double quotes to contain white space.
// Quotes may only appear at the beginning or end of the token.
//
// SplitUnquote treats '#' outside quotes as the beginning of a comment
// and drops everything after.
func SplitUnquote(s string) ([]string, error) {
	var tokens []string
	i := 0
	for i < len(s) {
		c := s[i]
		if c == ' ' || c == '\t' {
			i++
			continue
		}

		if c == '#' {
			break
		}

		if c == '\'' || c == '"' {
			end := i + 1
			for end < len(s) && s[end] != c {
				end++
			}
			if end == len(s) {
				return nil, fmt.Errorf("unterminated quote %c", c)
			}
			tokens = append(tokens, s[i+1:end])
			i = end + 1
			continue
		}

		end := i + 1
		for end < len(s) && s[end] != ' ' && s[end] != '\t' {
			end++
		}
		tokens = append(tokens, s[i:end])
		i = end
	}

	return tokens, nil
}
