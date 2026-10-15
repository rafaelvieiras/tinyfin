package api

import (
	_ "embed"
	"net/http"
	"time"
)

// localizationOptions is the list of interface languages the server offers
// (GET /Localization/Options): [{Name, Value}].
//
//go:embed localization_options.json
var localizationOptions []byte

// parentalRatings is the rating table GET /Localization/ParentalRatings
// serves: [{Name, Value, RatingScore{score, subScore}}] ordered by value.
//
//go:embed parental_ratings.json
var parentalRatings []byte

func writeRawJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// handleLocalizationOptions is GET /Localization/Options.
func (s *Server) handleLocalizationOptions(w http.ResponseWriter, r *http.Request) {
	writeRawJSON(w, localizationOptions)
}

// handleLocalizationParentalRatings is GET /Localization/ParentalRatings.
func (s *Server) handleLocalizationParentalRatings(w http.ResponseWriter, r *http.Request) {
	writeRawJSON(w, parentalRatings)
}

// handleSystemPing is GET and POST /System/Ping: the product name as a JSON
// string, with no authentication.
func (s *Server) handleSystemPing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ProductName)
}

// utcTimeDto mirrors the reference's UtcTimeResponse.
type utcTimeDto struct {
	RequestReceptionTime     wireTime `json:"RequestReceptionTime"`
	ResponseTransmissionTime wireTime `json:"ResponseTransmissionTime"`
}

// handleGetUtcTime is GET /GetUtcTime: the server clock when the request was
// received and when the response was sent, for clients that synchronise.
func (s *Server) handleGetUtcTime(w http.ResponseWriter, r *http.Request) {
	received := time.Now().UTC()
	writeJSON(w, http.StatusOK, utcTimeDto{RequestReceptionTime: wireTime{received}, ResponseTransmissionTime: wireTime{time.Now().UTC()}})
}
