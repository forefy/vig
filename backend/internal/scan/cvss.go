package scan

import (
	"errors"
	"math"
	"strings"
)

// CVSSBase computes the CVSS v3.x base score from a vector string.
func CVSSBase(vector string) (float64, error) {
	parts := strings.Split(vector, "/")
	if len(parts) < 9 || !strings.HasPrefix(parts[0], "CVSS:3") {
		return 0, errors.New("not a CVSS 3 vector")
	}
	m := map[string]string{}
	for _, p := range parts[1:] {
		if p == "" {
			continue // a trailing slash is tolerated
		}
		k, v, ok := strings.Cut(p, ":")
		if !ok || k == "" || v == "" {
			return 0, errors.New("malformed CVSS 3 vector")
		}
		m[k] = v
	}
	av := map[string]float64{"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2}[m["AV"]]
	ac := map[string]float64{"L": 0.77, "H": 0.44}[m["AC"]]
	ui := map[string]float64{"N": 0.85, "R": 0.62}[m["UI"]]
	scopeChanged := m["S"] == "C"
	var pr float64
	switch m["PR"] {
	case "N":
		pr = 0.85
	case "L":
		pr = 0.62
		if scopeChanged {
			pr = 0.68
		}
	case "H":
		pr = 0.27
		if scopeChanged {
			pr = 0.5
		}
	}
	cia := map[string]float64{"H": 0.56, "L": 0.22, "N": 0}
	c, i, a := cia[m["C"]], cia[m["I"]], cia[m["A"]]
	if av == 0 || ac == 0 || ui == 0 || pr == 0 {
		return 0, errors.New("incomplete CVSS vector")
	}
	iss := 1 - (1-c)*(1-i)*(1-a)
	var impact float64
	if scopeChanged {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	} else {
		impact = 6.42 * iss
	}
	exploitability := 8.22 * av * ac * pr * ui
	if impact <= 0 {
		return 0, nil
	}
	var score float64
	if scopeChanged {
		score = math.Min(1.08*(impact+exploitability), 10)
	} else {
		score = math.Min(impact+exploitability, 10)
	}
	return math.Ceil(score*10) / 10, nil
}
