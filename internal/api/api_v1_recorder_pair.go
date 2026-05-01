// /v1/recorder/pair — recorder-side handlers for the MS pairing
// flow. Driven by the Setup Wizard's WizPair component. The actual
// MS conversation runs in internal/pairing.Manager; these handlers
// wrap the manager's three operations: Start, Status, Reset.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/bluenviron/mediamtx/internal/pairing"
	"github.com/gin-gonic/gin"
)

func (a *API) onV1RecorderPairPost(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	if a.Pairing == nil {
		a.writeError(ctx, http.StatusServiceUnavailable,
			fmt.Errorf("pairing manager not initialized"))
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req pairing.StartRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	res, err := a.Pairing.Start(req)
	if err != nil {
		switch {
		case errors.Is(err, pairing.ErrAlreadyInProgress):
			a.writeError(ctx, http.StatusConflict, err)
		case errors.Is(err, pairing.ErrAlreadyPaired):
			a.writeError(ctx, http.StatusConflict, err)
		default:
			a.writeError(ctx, http.StatusBadRequest, err)
		}
		return
	}
	ctx.JSON(http.StatusAccepted, res)
}

func (a *API) onV1RecorderPairStatusGet(ctx *gin.Context) {
	if a.Pairing == nil {
		a.writeError(ctx, http.StatusServiceUnavailable,
			fmt.Errorf("pairing manager not initialized"))
		return
	}
	ctx.JSON(http.StatusOK, a.Pairing.Status())
}

func (a *API) onV1RecorderPairResetPost(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	if a.Pairing == nil {
		a.writeError(ctx, http.StatusServiceUnavailable,
			fmt.Errorf("pairing manager not initialized"))
		return
	}
	a.Pairing.Reset()
	a.writeOK(ctx)
}
