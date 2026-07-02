-- +goose Up

-- SP3: per-camera vendor event channel selection.
--   NULL/''  → auto (manufacturer-based resolution in internal/vendorevents)
--   'onvif'  → force ONVIF PullPoint
--   'amcrest'→ force Amcrest CGI attach stream
--   'none'   → no vendor event channel
ALTER TABLE cameras ADD COLUMN event_channel TEXT;

-- +goose Down
ALTER TABLE cameras DROP COLUMN event_channel;
