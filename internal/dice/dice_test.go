package dice

import (
	"errors"
	mrand "math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

const testSeed = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func mustParse(t *testing.T, s string) Expr {
	t.Helper()
	e, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return e
}

func mustRoll(t *testing.T, seed string, index int64, s string) Result {
	t.Helper()
	r, err := Roll(seed, index, mustParse(t, s))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParse(t *testing.T) {
	cases := map[string]Expr{
		"1d20+5":     {Text: "1d20+5", Terms: []Term{{1, 20}}, Modifier: 5},
		"2d6+1d4-1":  {Text: "2d6+1d4-1", Terms: []Term{{2, 6}, {1, 4}}, Modifier: -1},
		"d20":        {Text: "d20", Terms: []Term{{1, 20}}},
		"  3D6 + 2 ": {Text: "3D6 + 2", Terms: []Term{{3, 6}}, Modifier: 2},
		"-2+1d8":     {Text: "-2+1d8", Terms: []Term{{1, 8}}, Modifier: -2},
		"+1d6":       {Text: "+1d6", Terms: []Term{{1, 6}}},
		"1d6+3-1+2":  {Text: "1d6+3-1+2", Terms: []Term{{1, 6}}, Modifier: 4},
		"100d2":      {Text: "100d2", Terms: []Term{{100, 2}}},
		"1d1000":     {Text: "1d1000", Terms: []Term{{1, 1000}}},
		"50d6+50d6":  {Text: "50d6+50d6", Terms: []Term{{50, 6}, {50, 6}}},
		"1d20+10000": {Text: "1d20+10000", Terms: []Term{{1, 20}}, Modifier: 10000},
		"01d020+005": {Text: "01d020+005", Terms: []Term{{1, 20}}, Modifier: 5},
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): %v", in, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Parse(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{
		"", "   ", "d", "20", "5+3", "0d6", "101d6", "1d1", "1d0", "1d1001", "1d",
		"1d20++5", "1d20+", "+", "-1d4", "2d6-1d4", "1d20 5", "1 d20", "1d 20", "1d20 + 5 0", "1d20*2", "1d20/2",
		"2d6d6", "abc", "1d20+10001", "1e3d6", "1d6+-2", "51d6+50d6",
		"1d2" + strings.Repeat("+1", MaxTerms),
		strings.Repeat(" ", 1) + strings.Repeat("1", MaxExprBytes) + "d6",
		"１d6", // full-width digit
	} {
		if _, err := Parse(in); !errors.Is(err, ErrBadExpr) {
			t.Errorf("Parse(%q) err = %v, want ErrBadExpr", in, err)
		}
	}
}

// Pins exact results for a fixed seed. If this fails, rolls from saved
// sessions and simulations can no longer be reproduced: treat it as a
// breaking change to the dice, not as a test to update.
func TestGolden(t *testing.T) {
	want := []struct {
		expr  string
		dice  [][]int
		total int
	}{
		{"1d20+5", [][]int{{6}}, 11},
		{"2d6+1d4-1", [][]int{{1, 2}, {4}}, 6},
		{"4d6", [][]int{{5, 2, 2, 3}}, 12},
		{"1d1000", [][]int{{477}}, 477},
		{"1d20+5", [][]int{{13}}, 18},
	}
	for i, w := range want {
		r := mustRoll(t, testSeed, int64(i), w.expr)
		var dice [][]int
		for _, term := range r.Terms {
			dice = append(dice, term.Results)
		}
		if !reflect.DeepEqual(dice, w.dice) || r.Total != w.total {
			t.Errorf("roll %d %q = %v total %d, want %v total %d", i, w.expr, dice, r.Total, w.dice, w.total)
		}
	}
}

func TestRollIsDeterministic(t *testing.T) {
	seed := NewSeed()
	for i := range int64(50) {
		a := mustRoll(t, seed, i, "10d20+3")
		b := mustRoll(t, seed, i, "10d20+3")
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("roll %d differs between calls: %+v vs %+v", i, a, b)
		}
	}
}

func TestRollsDependOnIndexAndSeed(t *testing.T) {
	seed := NewSeed()
	base := mustRoll(t, seed, 0, "20d1000")
	if reflect.DeepEqual(base, mustRoll(t, seed, 1, "20d1000")) {
		t.Error("rolls 0 and 1 of one seed are identical")
	}
	if reflect.DeepEqual(base, mustRoll(t, NewSeed(), 0, "20d1000")) {
		t.Error("roll 0 of two different seeds is identical")
	}
}

func TestRollResultsAreWellFormed(t *testing.T) {
	seed := NewSeed()
	r := mustRoll(t, seed, 0, "3d6+2d1000+1d2-7")
	if r.Expr != "3d6+2d1000+1d2-7" || r.Modifier != -7 || len(r.Terms) != 3 {
		t.Fatalf("result = %+v", r)
	}
	sum := r.Modifier
	for _, term := range r.Terms {
		if len(term.Results) != term.Count {
			t.Errorf("term %dd%d has %d results", term.Count, term.Sides, len(term.Results))
		}
		for _, v := range term.Results {
			if v < 1 || v > term.Sides {
				t.Errorf("d%d rolled %d", term.Sides, v)
			}
			sum += v
		}
	}
	if r.Total != sum {
		t.Errorf("total = %d, want %d", r.Total, sum)
	}
}

// A coarse fairness check on a fixed seed, so it can't flake: 60,000 d6
// faces should each land within 3% of 10,000.
func TestRollIsRoughlyUniform(t *testing.T) {
	counts := make([]int, 7)
	e := mustParse(t, "100d6")
	for i := range int64(600) {
		r, err := Roll(testSeed, i, e)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range r.Terms[0].Results {
			counts[v]++
		}
	}
	for face := 1; face <= 6; face++ {
		if c := counts[face]; c < 9700 || c > 10300 {
			t.Errorf("face %d came up %d times in 60000, want about 10000", face, c)
		}
	}
}

func TestRollRejectsBadInput(t *testing.T) {
	e := mustParse(t, "1d20")
	for name, seed := range map[string]string{
		"short": "abcd", "not hex": strings.Repeat("zz", 32), "empty": "", "too long": testSeed + "00",
	} {
		if _, err := Roll(seed, 0, e); err == nil {
			t.Errorf("%s seed: Roll succeeded", name)
		}
	}
	if _, err := Roll(testSeed, -1, e); err == nil {
		t.Error("negative index: Roll succeeded")
	}
	if _, err := Roll(testSeed, 0, Expr{}); !errors.Is(err, ErrBadExpr) {
		t.Errorf("empty expression: err = %v, want ErrBadExpr", err)
	}
}

func TestNewSeed(t *testing.T) {
	a, b := NewSeed(), NewSeed()
	if len(a) != 64 || strings.Trim(a, "0123456789abcdef") != "" {
		t.Fatalf("NewSeed() = %q, want 64 lowercase hex characters", a)
	}
	if a == b {
		t.Fatal("two seeds are identical")
	}
}

func TestUniformIsInRange(t *testing.T) {
	src := mrand.NewChaCha8([32]byte{})
	for _, n := range []uint64{2, 3, 6, 7, 999, 1000} {
		for range 10000 {
			if v := uniform(src, n); v < 0 || uint64(v) >= n {
				t.Fatalf("uniform(%d) = %d", n, v)
			}
		}
	}
}
