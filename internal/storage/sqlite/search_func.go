package sqlite

import (
	"database/sql/driver"

	"modernc.org/sqlite"

	"github.com/rafaelvieiras/tinyfin/internal/storage"
)

func init() {
	// search_key(text) is storage.SearchKey: the form names are compared in
	// by searches. Evaluating it in Go keeps one normalisation rule.
	if err := sqlite.RegisterDeterministicScalarFunction("search_key", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			text, _ := args[0].(string)
			return storage.SearchKey(text), nil
		}); err != nil {
		panic(err)
	}
}
