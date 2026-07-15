package api

import (
	"errors"
	"net/http"
	"strings"
	"wwfc/database"
	"wwfc/gpcm"
)

const (
	defaultMKWManualRating = 5000
	minMKWManualRating     = 100
	maxMKWManualRating     = 1000000
	minMKWMMR              = 100
	maxMKWMMR              = 30000
)

var (
	ErrRatingType  = errors.New("rating type must be either 'vr', 'br', or 'mmr'")
	ErrRatingValue = errors.New("rating value must be between 100 and 1000000")
	ErrMMRValue    = errors.New("MMR value must be between 100 and 30000")
)

type SetMKWRatingRequest struct {
	Secret     string `json:"secret"`
	ProfileID  uint32 `json:"pid"`
	RatingType string `json:"rating_type"`
	Reason     string `json:"reason"`
	Value      int32  `json:"value"`
}

type SetMKWRatingResponse struct {
	User          database.User `json:"user"`
	RatingType    string        `json:"rating_type"`
	PreviousValue int32         `json:"previous_value"`
	Value         int32         `json:"value"`
	VR            int32         `json:"vr"`
	BR            int32         `json:"br"`
	MMR           int32         `json:"mmr"`
	Success       bool          `json:"success"`
	Error         string        `json:"error"`
}

var SetMKWRatingRoute = MakeRouteSpec[SetMKWRatingRequest, SetMKWRatingResponse](
	true,
	"/api/set_mkw_rating",
	func(req any, _ bool, _ *http.Request) (any, int, error) {
		return handleSetMKWRatingImpl(req.(SetMKWRatingRequest))
	},
	http.MethodPost,
)

func handleSetMKWRatingImpl(req SetMKWRatingRequest) (SetMKWRatingResponse, int, error) {
	if req.ProfileID == 0 {
		return SetMKWRatingResponse{}, http.StatusBadRequest, ErrPIDMissing
	}

	ratingType := strings.ToLower(strings.TrimSpace(req.RatingType))
	if ratingType != "vr" && ratingType != "br" && ratingType != "mmr" {
		return SetMKWRatingResponse{}, http.StatusBadRequest, ErrRatingType
	}

	if ratingType == "mmr" {
		if req.Value < minMKWMMR || req.Value > maxMKWMMR {
			return SetMKWRatingResponse{}, http.StatusBadRequest, ErrMMRValue
		}
	} else if req.Value < minMKWManualRating || req.Value > maxMKWManualRating {
		return SetMKWRatingResponse{}, http.StatusBadRequest, ErrRatingValue
	}

	currentVR := int32(defaultMKWManualRating)
	currentBR := int32(defaultMKWManualRating)
	currentMMR := int32(defaultMKWManualRating)

	storedVR, storedBR, err := database.GetMKWRawVRBR(pool, ctx, req.ProfileID)
	if err != nil {
		return SetMKWRatingResponse{}, http.StatusNotFound, ErrUserQuery
	}

	if storedVR != nil {
		currentVR = *storedVR
	}

	if storedBR != nil {
		currentBR = *storedBR
	}

	if storedMMR, found := database.GetMKWMMR(pool, ctx, req.ProfileID); found {
		currentMMR = storedMMR
	}

	reason := strings.TrimSpace(req.Reason)
	previousValue := int32(0)

	switch ratingType {
	case "vr":
		previousValue = currentVR
		currentVR = req.Value
	case "br":
		previousValue = currentBR
		currentBR = req.Value
	case "mmr":
		previousValue = currentMMR
		currentMMR = req.Value
	}

	var updateErr error
	if ratingType == "mmr" {
		updateErr = database.UpdateMKWMMR(pool, ctx, req.ProfileID, currentMMR)
	} else {
		updateErr = database.UpdateMKWVRBR(pool, ctx, req.ProfileID, currentVR, currentBR)
	}
	if updateErr != nil {
		return SetMKWRatingResponse{}, http.StatusInternalServerError, ErrTransaction
	}

	user, err := database.GetProfile(pool, ctx, req.ProfileID)
	if err != nil {
		return SetMKWRatingResponse{}, http.StatusInternalServerError, ErrUserQueryTransaction
	}

	kickReason := "Your " + strings.ToUpper(ratingType) + " was updated."
	if reason != "" {
		kickReason += " - " + reason
	}

	err = gpcm.KickPlayer(req.ProfileID, kickReason, gpcm.WWFCMsgKickedCustom)
	if err != nil {
		return SetMKWRatingResponse{}, http.StatusInternalServerError, err
	}

	return SetMKWRatingResponse{
		User:          user,
		RatingType:    ratingType,
		PreviousValue: previousValue,
		Value:         req.Value,
		VR:            currentVR,
		BR:            currentBR,
		MMR:           currentMMR,
	}, http.StatusOK, nil
}
