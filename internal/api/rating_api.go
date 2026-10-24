package api

import (
	"net/http"
	"strings"
)

// rating_api.go: the item rating routes (playstate.rating.*):
//
//	POST   /UserItems/{itemId}/Rating?likes=<bool>
//	DELETE /UserItems/{itemId}/Rating
//
// and their legacy per-user forms under /Users/{userId}/Items/{itemId}/Rating
// (thin aliases, the user id comes from the path). Both answer with the
// target user's data for the item and push it as a UserDataChanged message
// (playstate.push.R5).

// handleUserItemRatingSet is POST /UserItems/{itemId}/Rating.
func (s *Server) handleUserItemRatingSet(w http.ResponseWriter, r *http.Request) {
	s.userItemRating(w, r, caseInsensitiveQuery(r).Get("userid"), true)
}

// handleUserItemRatingDelete is DELETE /UserItems/{itemId}/Rating.
func (s *Server) handleUserItemRatingDelete(w http.ResponseWriter, r *http.Request) {
	s.userItemRating(w, r, caseInsensitiveQuery(r).Get("userid"), false)
}

// handleLegacyUserItemRatingSet is POST /Users/{userId}/Items/{itemId}/Rating.
func (s *Server) handleLegacyUserItemRatingSet(w http.ResponseWriter, r *http.Request) {
	s.userItemRating(w, r, r.PathValue("userId"), true)
}

// handleLegacyUserItemRatingDelete is DELETE /Users/{userId}/Items/{itemId}/Rating.
func (s *Server) handleLegacyUserItemRatingDelete(w http.ResponseWriter, r *http.Request) {
	s.userItemRating(w, r, r.PathValue("userId"), false)
}

// userItemRating stores a like (rating 10), a dislike (rating 1) or, for a
// delete or a set without a likes value, no rating at all. The parameter is
// validated before the item is resolved, like the other typed parameters.
func (s *Server) userItemRating(w http.ResponseWriter, r *http.Request, rawUserID string, set bool) {
	var likes *bool
	if set {
		if raw := caseInsensitiveQuery(r).Get("likes"); raw != "" {
			switch strings.ToLower(raw) {
			case "true":
				v := true
				likes = &v
			case "false":
				v := false
				likes = &v
			default:
				writeInvalidID(w, "likes", raw)
				return
			}
		}
	}
	target, item, ok := s.resolveUserItemFor(w, r, rawUserID)
	if !ok {
		return
	}
	state := s.getPlaystateOrZero(r, target.ID, item.ID)
	state.Likes, state.Rating = nil, nil
	if likes != nil {
		rating := 1.0
		if *likes {
			rating = 10
		}
		state.Likes, state.Rating = likes, &rating
	}
	s.writeUserData(w, r, target.ID, item, state)
}
