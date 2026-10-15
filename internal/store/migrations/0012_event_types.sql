-- +goose Up

-- Operator-extensible event type registry. Recorder seeds well-known
-- types on first boot via internal/events seedDefaults(); operators add
-- custom types via POST /v1/event-types with vendor='custom'.

CREATE TABLE event_types (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    vendor TEXT,                              -- onvif|amcrest|hikvision|reolink|internal|custom
    description TEXT
) STRICT;

INSERT INTO event_types (id, display_name, vendor, description) VALUES
    ('motion',         'Motion',          'internal', 'Motion detected on camera'),
    ('doorbell',       'Doorbell',        'internal', 'Doorbell pressed'),
    ('line_cross',     'Line Cross',      'onvif',    'ONVIF analytics line crossing'),
    ('tamper',         'Tamper',          'onvif',    'Camera tamper / scene change'),
    ('io_in',          'IO Input',        'onvif',    'Digital input asserted'),
    ('audio_alarm',    'Audio Alarm',     'onvif',    'Audio threshold exceeded'),
    ('person',         'Person',          'onvif',    'Person classification'),
    ('vehicle',        'Vehicle',         'onvif',    'Vehicle classification'),
    ('package',        'Package',         'onvif',    'Package classification'),
    ('animal',         'Animal',          'onvif',    'Animal classification'),
    ('camera_offline', 'Camera Offline',  'internal', 'RTSP source dropped'),
    ('camera_online',  'Camera Online',   'internal', 'RTSP source recovered');

-- +goose Down
DROP TABLE IF EXISTS event_types;
