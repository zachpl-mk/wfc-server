package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"wwfc/database"
)

type MKWRatingResponse struct {
	Found      int32 `json:"found"`
	VR         int32 `json:"vr"`
	BR         int32 `json:"br"`
	MMR        int32 `json:"mmr"`
	MMRRetro   int32 `json:"mmr_retro"`
	MMRCT      int32 `json:"mmr_ct"`
	MMRRegular int32 `json:"mmr_regular"`
}

func HandleMKWRatings(w http.ResponseWriter, r *http.Request) {
	u, err := url.Parse(r.URL.String())
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	pids := query["pid"]
	if len(pids) != 1 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	pid64, err := strconv.ParseUint(pids[0], 10, 32)
	if err != nil || pid64 == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	vr, br, found := database.GetMKWVRBR(pool, ctx, uint32(pid64))
	mmrRetro, mmrCT, mmrRegular, mmrFound := database.GetMKWMMRs(pool, ctx, uint32(pid64))
	response := MKWRatingResponse{
		Found:      0,
		VR:         0,
		BR:         0,
		MMR:        0,
		MMRRetro:   0,
		MMRCT:      0,
		MMRRegular: 0,
	}
	if found || mmrFound {
		response.Found = 1
	}
	if found {
		response.VR = vr
		response.BR = br
	}
	if mmrFound {
		response.MMR = mmrRetro
		response.MMRRetro = mmrRetro
		response.MMRCT = mmrCT
		response.MMRRegular = mmrRegular
	}

	jsonData, err := json.Marshal(response)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Length", strconv.Itoa(len(jsonData)))
	w.Write(jsonData)
}
