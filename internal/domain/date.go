package domain

import (
	"encoding/json"
	"time"
)

// DateLayout is the wire format of a Date.
const DateLayout = "2006-01-02"

// Date is a calendar date (no time of day), used for lot expiry dates.
// It is encoded as "YYYY-MM-DD" in JSON.
type Date struct{ t time.Time }

// NewDate returns the date for the given year, month and day.
func NewDate(year int, month time.Month, day int) Date {
	return Date{time.Date(year, month, day, 0, 0, 0, 0, time.UTC)}
}

// DateOf returns the UTC calendar date of t.
func DateOf(t time.Time) Date {
	t = t.UTC()
	return NewDate(t.Year(), t.Month(), t.Day())
}

// ParseDate parses a "YYYY-MM-DD" string.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return Date{}, Invalid(CodeValidation, "invalid date %q, want YYYY-MM-DD", s)
	}
	return Date{t}, nil
}

// Time returns the date as midnight UTC.
func (d Date) Time() time.Time { return d.t }

// IsZero reports whether d is the zero date.
func (d Date) IsZero() bool { return d.t.IsZero() }

// String formats the date as YYYY-MM-DD.
func (d Date) String() string { return d.t.Format(DateLayout) }

// Before reports whether d is strictly before o.
func (d Date) Before(o Date) bool { return d.t.Before(o.t) }

// Equal reports whether d and o are the same day.
func (d Date) Equal(o Date) bool { return d.t.Equal(o.t) }

// AddDays returns the date n days after d.
func (d Date) AddDays(n int) Date { return Date{d.t.AddDate(0, 0, n)} }

// DaysUntil returns the number of whole days from d to o (negative if o is
// before d).
func (d Date) DaysUntil(o Date) int { return int(o.t.Sub(d.t).Hours() / 24) }

// MarshalJSON implements json.Marshaler.
func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// UnmarshalJSON implements json.Unmarshaler.
func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
