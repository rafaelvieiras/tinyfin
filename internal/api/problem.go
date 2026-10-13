package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
)

// Failure bodies (wire.problem.R1-R3): a failure that a route does not
// describe with an explicit message of its own is an RFC 9110 problem
// document. These helpers are the single way handlers write a 400 so the
// body is consistent across routes.

const (
	problemTypeBadRequest = "https://tools.ietf.org/html/rfc9110#section-15.5.1"
	problemTypeNotFound   = "https://tools.ietf.org/html/rfc9110#section-15.5.5"
)

// writeProblemDoc writes a problem document with the given status, type,
// title and (when non-nil) an errors object naming the offending
// parameters.
func writeProblemDoc(w http.ResponseWriter, status int, typ, title string, errs map[string][]string) {
	trace := make([]byte, 24)
	copy(trace, randomID())
	body := map[string]any{
		"type":    typ,
		"title":   title,
		"status":  status,
		"traceId": "00-" + hex.EncodeToString(trace[:16]) + "-" + hex.EncodeToString(trace[16:24]) + "-00",
	}
	if errs != nil {
		body["errors"] = errs
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeBadRequest is a 400 the route raises without a message of its own
// (wire.problem.R3).
func writeBadRequest(w http.ResponseWriter) {
	writeProblemDoc(w, http.StatusBadRequest, problemTypeBadRequest, "Bad Request", nil)
}

// writeValidationErrors is a 400 for values that cannot be bound
// (wire.problem.R2); errs maps each offending parameter or body location
// to its messages.
func writeValidationErrors(w http.ResponseWriter, errs map[string][]string) {
	writeProblemDoc(w, http.StatusBadRequest, problemTypeBadRequest, "One or more validation errors occurred.", errs)
}

// writeValidationProblem names a single offending parameter.
func writeValidationProblem(w http.ResponseWriter, field, message string) {
	writeValidationErrors(w, map[string][]string{field: {message}})
}

// writeInvalidID is a path/query identifier that is not a GUID.
func writeInvalidID(w http.ResponseWriter, field, value string) {
	writeValidationProblem(w, field, "The value '"+value+"' is not valid.")
}

// writeInvalidBody is a request body that is not valid JSON for the route.
func writeInvalidBody(w http.ResponseWriter) {
	writeValidationProblem(w, "$", "The request body is missing or is not valid JSON.")
}
