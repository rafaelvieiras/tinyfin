package sqlite

import (
	"database/sql/driver"

	"modernc.org/sqlite"

	"github.com/rafaelvieiras/tinyfin/internal/rating"
)

// ratingScoreInt packs a rating score into one comparable integer: the
// value in the thousands, the sub rating below it.
func ratingScoreInt(s rating.Score) int {
	sub := s.Sub
	if sub > 999 {
		sub = 999
	}
	if sub < 0 {
		sub = 0
	}
	return s.Value*1000 + sub
}

func init() {
	// parental_score(text) is the packed rating.Parse score of an
	// official-rating text, or NULL when the text has no value (unrated).
	// Evaluating it in Go keeps the parsing rules in one place and avoids a
	// stored, stale copy.
	if err := sqlite.RegisterDeterministicScalarFunction("parental_score", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			text, _ := args[0].(string)
			sc, ok := rating.Parse(text)
			if !ok {
				return nil, nil
			}
			return int64(ratingScoreInt(sc)), nil
		}); err != nil {
		panic(err)
	}
}
