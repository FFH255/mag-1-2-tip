package service

import "testing"

func TestSanitizeText_EscapesHTMLSpecials(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello", "hello"},
		{"<script>alert(1)</script>", "&lt;script&gt;alert(1)&lt;/script&gt;"},
		{`"onerror=x"`, "&quot;onerror=x&quot;"},
		{"a>b<c", "a&gt;b&lt;c"},
		{"it's ok", "it&#39;s ok"},
	}
	for _, c := range cases {
		if got := sanitizeText(c.in); got != c.want {
			t.Errorf("sanitizeText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
