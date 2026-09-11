package store

import (
	"encoding/json"
	"time"
)

type IsochroneKey struct {
	CompileJobID string `json:"compile_job_id"`
	StationSlug  string `json:"station_slug"`
	Mode         string `json:"mode"`
	ContourMins  int    `json:"contour_mins"`
	DepartsOn    string `json:"departs_on,omitempty"`
}

type CachedIsochrone struct {
	Key       IsochroneKey    `json:"key"`
	Geometry  json.RawMessage `json:"geometry"`
	TilesetAt time.Time       `json:"tileset_at,omitempty"`
}

type CacheLookupRequest struct {
	Keys []IsochroneKey `json:"keys"`
}

type CacheLookupEntry struct {
	Key      IsochroneKey    `json:"key"`
	Geometry json.RawMessage `json:"geometry"`
}

type CacheLookupResponse struct {
	Entries []CacheLookupEntry `json:"entries"`
}

type CachePutRequest struct {
	Entries []CachedIsochrone `json:"entries"`
}

type JobSucceededBody struct {
	Result json.RawMessage `json:"result"`
}

type JobFailedBody struct {
	Error string `json:"error"`
}
