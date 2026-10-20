package desktop

import "errors"

// failure is an error with two readers. Its Error is a sentence for the
// person looking at the app — the state's "error" field — and its detail is
// the technical cause (a URL, an exit status, a wrapped Go error), which goes
// in the failed step's line and the daemon log. Keeping them apart is what
// stops "X: could not X: Y" chains from reaching the screen.
type failure struct {
	plain  string
	detail error
}

func (f *failure) Error() string { return f.plain }
func (f *failure) Unwrap() error { return f.detail }

// fail builds a failure. detail may be nil when the sentence says it all.
func fail(plain string, detail error) error {
	return &failure{plain: plain, detail: detail}
}

// explain splits err into the sentence for people and the detail for the
// step's line. An error that is not a failure gets the fallback sentence and
// keeps its own text as the detail.
func explain(err error, fallback string) (plain, detail string) {
	var f *failure
	if !errors.As(err, &f) {
		return fallback, err.Error()
	}
	plain = f.plain
	// A failure can wrap another, when a step passes one on; the detail is
	// the innermost cause, which is the one with the technical words.
	for f.detail != nil {
		var inner *failure
		if !errors.As(f.detail, &inner) {
			return plain, f.detail.Error()
		}
		f = inner
	}
	return plain, ""
}
