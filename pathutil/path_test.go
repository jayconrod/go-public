package pathutil

import (
	"runtime"
	"testing"
)

func TestHasPathPrefix(t *testing.T) {
	for _, test := range []struct {
		desc, path, prefix string
		want               bool
	}{
		{
			desc:   "empty_prefix",
			path:   "a/b",
			prefix: "",
			want:   true,
		}, {
			desc:   "partial_prefix",
			path:   "a/b",
			prefix: "a",
			want:   true,
		}, {
			desc:   "full_prefix",
			path:   "a/b",
			prefix: "a/b",
			want:   true,
		}, {
			desc:   "partial_component",
			path:   "aa/b",
			prefix: "a",
			want:   false,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			if got := HasPathPrefix(test.path, test.prefix); got != test.want {
				t.Errorf("HasPathPrefix(%q, %q): got %v, want %v", test.path, test.prefix, got, test.want)
			}
		})
	}
}

func TestHasFilePathPrefix(t *testing.T) {
	type test struct {
		desc, path, prefix string
		want               bool
	}
	var tests []test
	if runtime.GOOS == "windows" {
		tests = []test{
			{
				desc:   "empty_prefix",
				path:   `c:\a\b`,
				prefix: "",
				want:   true,
			}, {
				desc:   "drive_prefix",
				path:   `c:\a\b`,
				prefix: `c:\`,
				want:   true,
			}, {
				desc:   "partial_prefix",
				path:   `c:\a\b`,
				prefix: `c:\a`,
				want:   true,
			}, {
				desc:   "full_prefix",
				path:   `c:\a\b`,
				prefix: `c:\a\b`,
				want:   true,
			}, {
				desc:   "partial_component",
				path:   `c:\aa\b`,
				prefix: `c:\a`,
				want:   false,
			},
		}
	} else {
		tests = []test{
			{
				desc:   "empty_prefix",
				path:   "/a/b",
				prefix: "",
				want:   true,
			}, {
				desc:   "partial_prefix",
				path:   "/a/b",
				prefix: "/a",
				want:   true,
			}, {
				desc:   "full_prefix",
				path:   "/a/b",
				prefix: "/a/b",
				want:   true,
			}, {
				desc:   "partial_component",
				path:   "/aa/b",
				prefix: "/a",
				want:   false,
			},
		}
	}
	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			if got := HasFilePathPrefix(test.path, test.prefix); got != test.want {
				t.Errorf("HasFilePathPrefix(%q, %q): got %v, want %v", test.path, test.prefix, got, test.want)
			}
		})
	}
}

func TestTrimFilePathPrefix(t *testing.T) {
	type test struct {
		desc, path, prefix, want string
	}
	var tests []test
	if runtime.GOOS == "windows" {
		tests = []test{
			// Note: these two cases in which the result preserves the leading \
			// don't come up in reality in gorelease. That's because prefix is
			// always far to the right of the path parts (ex github.com/foo/bar
			// in C:\Users\foo\AppData\Local\Temp\...\github.com\foo\bar).
			{
				desc:   "empty_prefix",
				path:   `c:\a\b`,
				prefix: "",
				want:   `\a\b`,
			}, {
				desc:   "partial_component",
				path:   `c:\aa\b`,
				prefix: `c:\a`,
				want:   `\aa\b`,
			},

			{
				desc:   "drive_prefix",
				path:   `c:\a\b`,
				prefix: `c:\`,
				want:   `a\b`,
			}, {
				desc:   "partial_prefix",
				path:   `c:\a\b`,
				prefix: `c:\a`,
				want:   `b`,
			}, {
				desc:   "full_prefix",
				path:   `c:\a\b`,
				prefix: `c:\a\b`,
				want:   "",
			},
		}
	} else {
		tests = []test{
			{
				desc:   "empty_prefix",
				path:   "/a/b",
				prefix: "",
				want:   "/a/b",
			}, {
				desc:   "partial_prefix",
				path:   "/a/b",
				prefix: "/a",
				want:   "b",
			}, {
				desc:   "full_prefix",
				path:   "/a/b",
				prefix: "/a/b",
				want:   "",
			}, {
				desc:   "partial_component",
				path:   "/aa/b",
				prefix: "/a",
				want:   "/aa/b",
			},
		}
	}
	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			if got := TrimFilePathPrefix(test.path, test.prefix); got != test.want {
				t.Errorf("TrimFilePathPrefix(%q, %q): got %v, want %v", test.path, test.prefix, got, test.want)
			}
		})
	}
}

func TestTrimPathPrefix(t *testing.T) {
	for _, test := range []struct {
		desc, path, prefix, want string
	}{
		{
			desc:   "empty_prefix",
			path:   "a/b",
			prefix: "",
			want:   "a/b",
		}, {
			desc:   "abs_empty_prefix",
			path:   "/a/b",
			prefix: "",
			want:   "/a/b",
		}, {
			desc:   "partial_prefix",
			path:   "a/b",
			prefix: "a",
			want:   "b",
		}, {
			desc:   "full_prefix",
			path:   "a/b",
			prefix: "a/b",
			want:   "",
		}, {
			desc:   "partial_component",
			path:   "aa/b",
			prefix: "a",
			want:   "aa/b",
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			if got := TrimPathPrefix(test.path, test.prefix); got != test.want {
				t.Errorf("TrimPathPrefix(%q, %q): got %q, want %q", test.path, test.prefix, got, test.want)
			}
		})
	}
}

func TestPathLen(t *testing.T) {
	for _, test := range []struct {
		path string
		want int
	}{
		{"", 0},
		{"/", 0},
		{"a", 1},
		{"a/", 1},
		{"/a", 1},
		{"//a", 2},
		{"a//", 2},
		{"a/b", 2},
		{"a//b", 3},
		{"/a//b", 3},
		{"a//b/", 3},
		{"//a//b//", 5},
	} {
		if got := PathLen(test.path); got != test.want {
			t.Errorf("PathLen(%q): got %d, want %d", test.path, got, test.want)
		}
	}
}

func TestPathComponent(t *testing.T) {
	for _, test := range []struct {
		path string
		i    int
		want string
	}{
		{"a", 0, "a"},
		{"a/", 0, "a"},
		{"/a", 0, "a"},
		{"//a", 0, ""},
		{"//a", 1, "a"},
		{"a//", 0, "a"},
		{"a//", 1, ""},
		{"a/b", 0, "a"},
		{"a/b", 1, "b"},
		{"a//b", 0, "a"},
		{"a//b", 1, ""},
		{"a//b", 2, "b"},
	} {
		if got := PathComponent(test.path, test.i); got != test.want {
			t.Errorf("PathComponent(%q, %d): got %q, want %q", test.path, test.i, got, test.want)
		}
	}
}

func TestQuoteForShell(t *testing.T) {
	for _, test := range []struct {
		path, want string
	}{
		{``, `""`},
		{`a`, `a`},
		{`a/b`, `a/b`},
		{`a b`, `"a b"`},
		{`"ab"`, `"\"ab\""`},
	} {
		if got := QuoteForShell(test.path); got != test.want {
			t.Errorf("QuoteForShell(%q): got %q, want %q", test.path, got, test.want)
		}
	}
}
