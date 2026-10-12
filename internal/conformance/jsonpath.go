package conformance

import (
	"fmt"
	"strconv"
	"strings"
)

// evalPath evaluates a minimal JSONPath subset against a decoded JSON value:
//
//	$            the root
//	$.a.b        object member access
//	$.a[0]       array index
//	$.a[*].b     wildcard over an array; the result is the list of matches
//	$.a.length   NOT supported; use the "len" operator instead
//
// It returns the value, whether it was found, and an error for malformed paths.
// A wildcard path always returns a []any (possibly empty) and found=true when
// the array itself exists.
func evalPath(root any, path string) (any, bool, error) {
	if path == "" || path == "$" {
		return root, true, nil
	}
	if !strings.HasPrefix(path, "$") {
		return nil, false, fmt.Errorf("path must start with $: %q", path)
	}
	toks, err := tokenize(path[1:])
	if err != nil {
		return nil, false, err
	}
	cur := []any{root}
	wild := false
	for _, t := range toks {
		var next []any
		for _, c := range cur {
			switch {
			case t == "*":
				arr, ok := c.([]any)
				if !ok {
					return nil, false, nil
				}
				wild = true
				next = append(next, arr...)
			case strings.HasPrefix(t, "#"):
				idx, _ := strconv.Atoi(t[1:])
				arr, ok := c.([]any)
				if !ok || idx < 0 || idx >= len(arr) {
					return nil, false, nil
				}
				next = append(next, arr[idx])
			default:
				m, ok := c.(map[string]any)
				if !ok {
					return nil, false, nil
				}
				v, ok := m[t]
				if !ok {
					return nil, false, nil
				}
				next = append(next, v)
			}
		}
		cur = next
	}
	if wild {
		return cur, true, nil
	}
	if len(cur) == 0 {
		return nil, false, nil
	}
	return cur[0], true, nil
}

func tokenize(s string) ([]string, error) {
	var out []string
	for i := 0; i < len(s); {
		switch s[i] {
		case '.':
			i++
			j := i
			for j < len(s) && s[j] != '.' && s[j] != '[' {
				j++
			}
			if j == i {
				return nil, fmt.Errorf("empty member in path")
			}
			out = append(out, s[i:j])
			i = j
		case '[':
			j := strings.IndexByte(s[i:], ']')
			if j < 0 {
				return nil, fmt.Errorf("unterminated [ in path")
			}
			inner := s[i+1 : i+j]
			if inner == "*" {
				out = append(out, "*")
			} else if _, err := strconv.Atoi(inner); err == nil {
				out = append(out, "#"+inner)
			} else {
				return nil, fmt.Errorf("unsupported index %q", inner)
			}
			i += j + 1
		default:
			return nil, fmt.Errorf("unexpected %q in path", s[i])
		}
	}
	return out, nil
}
