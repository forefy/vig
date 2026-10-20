package scan

import (
	"math"
	"strings"
	"testing"
)

func TestCVSSBase(t *testing.T) {
	cases := []struct {
		vector  string
		want    float64
		wantErr bool
	}{
		// Reference scores from the FIRST calculator.
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8, false},
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H", 10.0, false},
		{"CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:H", 7.8, false},
		{"CVSS:3.1/AV:N/AC:H/PR:N/UI:N/S:U/C:H/I:N/A:N", 5.9, false},
		{"CVSS:3.0/AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N", 5.3, false},
		{"CVSS:3.1/AV:N/AC:L/PR:L/UI:R/S:C/C:L/I:L/A:N", 5.4, false},
		{"CVSS:3.1/AV:P/AC:H/PR:H/UI:R/S:U/C:L/I:N/A:N", 1.6, false},
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N", 0, false},
		{"CVSS:3.1/AV:A/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H", 6.5, false},
		{"CVSS:3.1/AV:N/AC:L/PR:H/UI:N/S:C/C:H/I:H/A:H", 9.1, false},
		// Extra metrics are tolerated.
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H/E:P/RL:O/RC:C", 9.8, false},
		// Rejections.
		{"", 0, true},
		{"CVSS:2.0/AV:N/AC:L/Au:N/C:P/I:P/A:P", 0, true},
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H", 0, true},
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H/", 9.8, false}, // trailing slash is harmless
		{"CVSS:3.1/AV:X/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 0, true},     // unknown AV
		{"CVSS:3.1/AV:N/AC:L/PR:X/UI:N/S:U/C:H/I:H/A:H", 0, true},     // unknown PR
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:X/S:U/C:H/I:H/A:H", 0, true},     // unknown UI
		{"CVSS:3.1/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H/E:P", 0, true},      // AV missing
		{"AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H/E:P", 0, true},          // no prefix
	}
	for _, c := range cases {
		got, err := CVSSBase(c.vector)
		if (err != nil) != c.wantErr {
			t.Errorf("CVSSBase(%q) err=%v wantErr=%v", c.vector, err, c.wantErr)
			continue
		}
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("CVSSBase(%q)=%v want %v", c.vector, got, c.want)
		}
	}
}

func TestSeverityBanding(t *testing.T) {
	cases := []struct {
		name string
		adv  Advisory
		want string
	}{
		{"critical", Advisory{CVSS: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}, "critical"},
		{"critical at 9.0 boundary", Advisory{CVSS: "CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:C/C:H/I:H/A:N"}, "critical"}, // 9.6
		{"high", Advisory{CVSS: "CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:H"}, "high"},
		{"medium", Advisory{CVSS: "CVSS:3.0/AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N"}, "medium"},
		{"medium at 6.5", Advisory{CVSS: "CVSS:3.1/AV:A/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H"}, "medium"},
		{"low", Advisory{CVSS: "CVSS:3.1/AV:P/AC:H/PR:H/UI:R/S:U/C:L/I:N/A:N"}, "low"},
		{"zero impact is info", Advisory{CVSS: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N"}, "info"},
		{"informational wins over cvss", Advisory{Informational: "unmaintained", CVSS: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}, "info"},
		{"no cvss", Advisory{}, "unknown"},
		{"bad cvss", Advisory{CVSS: "CVSS:2.0/AV:N"}, "unknown"},
	}
	for _, c := range cases {
		if got := c.adv.Severity(); got != c.want {
			t.Errorf("%s: %q want %q", c.name, got, c.want)
		}
	}
	// Sanity-check the boundary claim used above.
	if s, _ := CVSSBase("CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:C/C:H/I:H/A:N"); s < 9 {
		t.Fatalf("boundary vector scores %v, expected >= 9", s)
	}
}

func TestAdvisoryURL(t *testing.T) {
	a := Advisory{ID: "RUSTSEC-2024-0001"}
	if !strings.HasSuffix(a.URL(), "/RUSTSEC-2024-0001.html") || !strings.HasPrefix(a.URL(), "https://rustsec.org/advisories/") {
		t.Fatal(a.URL())
	}
}
