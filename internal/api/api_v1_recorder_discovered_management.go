// /v1/recorder/discovered-management — surface mDNS-discovered MS
// advertisements to the recorder's setup wizard, replacing the
// previously-mocked discovery pool.

package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (a *API) onV1RecorderDiscoveredManagementGet(ctx *gin.Context) {
	if a.MDNS == nil {
		ctx.JSON(http.StatusOK, gin.H{"items": []any{}})
		return
	}
	items := a.MDNS.Discovered()
	if items == nil {
		items = nil // ensure []DiscoveredManagement{} marshals to []
	}
	out := make([]any, 0, len(items))
	for i := range items {
		out = append(out, items[i])
	}
	ctx.JSON(http.StatusOK, gin.H{"items": out})
}
