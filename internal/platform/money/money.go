// Package money holds the small set of decimal-safe helpers for summing/rounding the numeric(15,2)
// money columns that flow through this app as strings (never float64, to avoid rounding error —
// same reasoning already applied independently in internal/loans/schedule.go and
// internal/finance/service.go's amountsDiffer). Extracted here once a third module (reports, this
// pass) needed the same primitive, rather than re-copying the math/big.Rat boilerplate a third time.
package money

import (
	"errors"
	"math/big"
)

// Parse reads a numeric(15,2)-shaped string into a big.Rat.
func Parse(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, errors.New("not a valid decimal")
	}
	return r, nil
}

// Round2 rounds r to 2 decimal places, half-away-from-zero — see (*big.Rat).FloatString's
// documented rounding behavior.
func Round2(r *big.Rat) *big.Rat {
	rounded, _ := new(big.Rat).SetString(r.FloatString(2))
	return rounded
}

// Format renders r as a fixed 2-decimal string (e.g. "1500.00").
func Format(r *big.Rat) string {
	return r.FloatString(2)
}

// Zero is "0.00".
const Zero = "0.00"

// Add sums two numeric(15,2)-shaped strings, returning a fixed 2-decimal string.
func Add(a, b string) (string, error) {
	ra, err := Parse(a)
	if err != nil {
		return "", err
	}
	rb, err := Parse(b)
	if err != nil {
		return "", err
	}
	return Format(Round2(new(big.Rat).Add(ra, rb))), nil
}

// Sub subtracts b from a, returning a fixed 2-decimal string.
func Sub(a, b string) (string, error) {
	ra, err := Parse(a)
	if err != nil {
		return "", err
	}
	rb, err := Parse(b)
	if err != nil {
		return "", err
	}
	return Format(Round2(new(big.Rat).Sub(ra, rb))), nil
}

// Div divides a by an integer divisor, rounding to decimals places.
func Div(a string, divisor int64, decimals int) (string, error) {
	ra, err := Parse(a)
	if err != nil {
		return "", err
	}
	q := new(big.Rat).Quo(ra, big.NewRat(divisor, 1))
	rounded, _ := new(big.Rat).SetString(q.FloatString(decimals))
	return rounded.FloatString(decimals), nil
}

// Compare returns -1, 0, or 1 as a is numerically less than, equal to, or greater than b.
func Compare(a, b string) (int, error) {
	ra, err := Parse(a)
	if err != nil {
		return 0, err
	}
	rb, err := Parse(b)
	if err != nil {
		return 0, err
	}
	return ra.Cmp(rb), nil
}

// Sign returns -1, 0, or 1 as s is negative, zero, or positive.
func Sign(s string) (int, error) {
	r, err := Parse(s)
	if err != nil {
		return 0, err
	}
	return r.Sign(), nil
}
