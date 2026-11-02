// Package dice is the only source of game randomness (invariant 3).
//
// Each session has a secret 32-byte seed. Roll number i of a session (0 for
// its first roll) draws from a ChaCha8 generator keyed by
// SHA-256(domain, seed, i), so the results depend only on the seed, the roll's
// position, and the expression. The fold just counts rolls: a restarted server
// continues the same sequence without re-rolling anything, and replay reads
// results from the log.
package dice

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	mrand "math/rand/v2"
	"strconv"
	"strings"
)

// Limits on a dice expression (ADR-003).
const (
	MaxCount     = 100   // dice in one term, and in the whole expression
	MinSides     = 2     //
	MaxSides     = 1000  //
	MaxTerms     = 20    // dice terms plus modifiers
	MaxModifier  = 10000 // absolute value of one integer modifier
	MaxExprBytes = 100
)

// ErrBadExpr means a dice expression does not parse or is out of range.
var ErrBadExpr = errors.New("dice: bad dice expression")

// domain separates this use of the seed from any other.
const domain = "chartroom/dice/v1"

// NewSeed returns a new session seed: 32 bytes from crypto/rand, as 64 hex
// characters. It is secret: anyone holding it can predict every roll.
func NewSeed() string {
	var b [32]byte
	_, _ = rand.Read(b[:]) // never fails (Go 1.24+)
	return hex.EncodeToString(b[:])
}

// Term is one NdM group in an expression.
type Term struct {
	Count int
	Sides int
}

// Expr is a parsed dice expression: a sum of dice terms and integer modifiers.
type Expr struct {
	Text     string // as typed, trimmed
	Terms    []Term
	Modifier int // sum of the integer parts
}

// Parse reads expressions such as "1d20+5", "2d6+1d4-1", or "d20". Dice
// terms are added; integers may be added or subtracted. "d" is case-insensitive,
// and spaces are allowed around + and - but not inside a term.
func Parse(s string) (Expr, error) {
	text := strings.TrimSpace(s)
	if text == "" {
		return Expr{}, fmt.Errorf("%w: empty", ErrBadExpr)
	}
	if len(text) > MaxExprBytes {
		return Expr{}, fmt.Errorf("%w: longer than %d characters", ErrBadExpr, MaxExprBytes)
	}

	// Spaces may sit around + and -, never inside a term: "1d20 5" must not
	// quietly become "1d205".
	if strings.Contains(text, " ") {
		for _, field := range strings.FieldsFunc(text, func(r rune) bool { return r == '+' || r == '-' }) {
			if strings.Contains(strings.TrimSpace(field), " ") {
				return Expr{}, fmt.Errorf("%w: space inside %q", ErrBadExpr, strings.TrimSpace(field))
			}
		}
	}
	compact := strings.ToLower(strings.ReplaceAll(text, " ", ""))
	e := Expr{Text: text}
	dice, parts := 0, 0
	for i := 0; i < len(compact); {
		sign := 1
		if i > 0 || compact[0] == '+' || compact[0] == '-' {
			switch compact[i] {
			case '+':
			case '-':
				sign = -1
			default:
				return Expr{}, fmt.Errorf("%w: expected + or - at %q", ErrBadExpr, compact[i:])
			}
			i++
		}
		j := i
		for j < len(compact) && compact[j] != '+' && compact[j] != '-' {
			j++
		}
		part := compact[i:j]
		i = j

		if parts++; parts > MaxTerms {
			return Expr{}, fmt.Errorf("%w: more than %d terms", ErrBadExpr, MaxTerms)
		}
		if count, sides, ok := strings.Cut(part, "d"); ok {
			if sign < 0 {
				return Expr{}, fmt.Errorf("%w: dice can only be added, not subtracted (%q)", ErrBadExpr, part)
			}
			n := 1
			if count != "" {
				var err error
				if n, err = number(count); err != nil || n < 1 || n > MaxCount {
					return Expr{}, fmt.Errorf("%w: dice count in %q must be 1 to %d", ErrBadExpr, part, MaxCount)
				}
			}
			m, err := number(sides)
			if err != nil || m < MinSides || m > MaxSides {
				return Expr{}, fmt.Errorf("%w: sides in %q must be %d to %d", ErrBadExpr, part, MinSides, MaxSides)
			}
			if dice += n; dice > MaxCount {
				return Expr{}, fmt.Errorf("%w: more than %d dice in total", ErrBadExpr, MaxCount)
			}
			e.Terms = append(e.Terms, Term{Count: n, Sides: m})
			continue
		}
		k, err := number(part)
		if err != nil || k > MaxModifier {
			return Expr{}, fmt.Errorf("%w: %q is not a die or a number up to %d", ErrBadExpr, part, MaxModifier)
		}
		e.Modifier += sign * k
	}
	if len(e.Terms) == 0 {
		return Expr{}, fmt.Errorf("%w: no dice", ErrBadExpr)
	}
	return e, nil
}

// number parses a non-empty run of ASCII digits.
func number(s string) (int, error) {
	if s == "" || strings.TrimLeft(s, "0123456789") != "" {
		return 0, ErrBadExpr
	}
	return strconv.Atoi(s)
}

// TermResult is one term and the face each die landed on.
type TermResult struct {
	Count   int
	Sides   int
	Results []int
}

// Result is a finished roll.
type Result struct {
	Expr     string
	Terms    []TermResult
	Modifier int
	Total    int
}

// Roll rolls e as roll number index of the session with seed seedHex. The
// same seed, index, and expression always give the same result.
func Roll(seedHex string, index int64, e Expr) (Result, error) {
	seed, err := hex.DecodeString(seedHex)
	if err != nil || len(seed) != 32 {
		return Result{}, errors.New("dice: seed must be 64 hex characters")
	}
	if index < 0 {
		return Result{}, errors.New("dice: negative roll index")
	}
	if len(e.Terms) == 0 {
		return Result{}, fmt.Errorf("%w: no dice", ErrBadExpr)
	}

	h := sha256.New()
	h.Write([]byte(domain))
	h.Write(seed)
	_ = binary.Write(h, binary.BigEndian, index)
	var key [32]byte
	copy(key[:], h.Sum(nil))
	src := mrand.NewChaCha8(key)

	res := Result{Expr: e.Text, Modifier: e.Modifier, Total: e.Modifier}
	for _, t := range e.Terms {
		tr := TermResult{Count: t.Count, Sides: t.Sides, Results: make([]int, t.Count)}
		for i := range tr.Results {
			tr.Results[i] = 1 + uniform(src, uint64(t.Sides))
			res.Total += tr.Results[i]
		}
		res.Terms = append(res.Terms, tr)
	}
	return res, nil
}

// uniform returns an unbiased integer in [0, n) by rejection sampling. It is
// written out here, rather than using rand.IntN, so results cannot change if
// the standard library's algorithm does.
func uniform(src *mrand.ChaCha8, n uint64) int {
	// Reject the top partial block of values so every residue is equally likely.
	limit := math.MaxUint64 - (math.MaxUint64%n+1)%n
	for {
		if v := src.Uint64(); v <= limit {
			return int(v % n)
		}
	}
}
