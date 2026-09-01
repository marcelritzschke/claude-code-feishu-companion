package pathdisp

import "testing"

func TestHome(t *testing.T) {
	original := homeDir
	homeDir = func() string { return "/home/masai" }
	defer func() { homeDir = original }()

	cases := []struct {
		name string
		path string
		want string
	}{
		{"inside home", "/home/masai/work/payments-api", "~/work/payments-api"},
		{"home itself", "/home/masai", "~"},
		{"outside home", "/srv/checkouts/api", "/srv/checkouts/api"},
		{"a sibling that shares the prefix", "/home/masaix/work", "/home/masaix/work"},
		{"windows separators", `C:\Users\masai\work`, `C:\Users\masai\work`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Home(tc.path); got != tc.want {
				t.Errorf("Home(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// Without a readable home directory the path is shown as it is, rather than
// mangled against an empty prefix.
func TestHomeWithoutAHomeDirectory(t *testing.T) {
	original := homeDir
	homeDir = func() string { return "" }
	defer func() { homeDir = original }()

	if got := Home("/home/masai/work"); got != "/home/masai/work" {
		t.Errorf("Home = %q, want the path unchanged", got)
	}
}
