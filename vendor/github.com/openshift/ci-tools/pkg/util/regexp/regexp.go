package regexp

import "regexp"

// Regexp is a wrapper for regexp.Regexp that is also comparable.
// This type can be used as a key in maps: `map[Regexp]T`.
type Regexp struct {
	expr    string
	Pattern *regexp.Regexp
}

// AppendText implements [encoding.TextAppender].
func (re *Regexp) AppendText(b []byte) ([]byte, error) {
	return append(b, re.Pattern.String()...), nil
}

// MarshalText implements [encoding.TextMarshaler].
func (re Regexp) MarshalText() ([]byte, error) {
	return re.Pattern.AppendText(nil)
}

// UnmarshalText implements [encoding.UnmarshalText].
func (re *Regexp) UnmarshalText(text []byte) error {
	re.expr = string(text)
	compiled, err := regexp.Compile(re.expr)
	if err != nil {
		return err
	}
	re.Pattern = compiled
	return nil

}

func Compile(expr string) (*Regexp, error) {
	compiled, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	return &Regexp{expr: expr, Pattern: compiled}, nil
}

// LookupByMatch scans <key, value> tuples `patterns` and returns the value
// whose key matches `s`.
func LookupByMatch[T any](patterns map[Regexp]T, s string) (T, bool) {
	for re, val := range patterns {
		if re.Pattern.Match([]byte(s)) {
			return val, true
		}
	}

	var zero T
	return zero, false
}
