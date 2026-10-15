// Events route (SP3 + SP4) — reverse-chronological camera-event feed
// backed by the canonical events store. Each row carries a snapshot
// thumbnail (signed, short-lived media URL), the event type + camera,
// severity accent, and acknowledge state. Row actions acknowledge the
// event or lazily export a clip around it (POST /v1/events/:id/clip).
//
// Live updates ride the /v1/events/stream SSE channel: any message
// simply re-fetches the current page (simple + always correct against
// the filters). If the EventSource errors we fall back to a 30s poll.

import { useCallback, useEffect, useRef, useState } from 'react';
import { PageHeader } from '../components/PageHeader';
import { Btn, Card, SectionHeader, Toggle } from '../components/primitives';
import { Icon } from '../components/Icon';
import {
  ApiError,
  fetchCameras,
  listEvents,
  listEventTypes,
  ackEvent,
  createEventClip,
  subscribeEventsStream,
} from '../lib/api';
import type { Camera, EventRecord } from '../lib/api';
import type { EventType, ToastInput } from '../lib/types';

interface Props {
  addToast: (t: ToastInput) => void;
}

const selectStyle: React.CSSProperties = {
  background: 'var(--bg-tertiary)',
  border: '1px solid var(--border)',
  borderRadius: 4,
  padding: '8px 10px',
  color: 'var(--text-primary)',
  fontFamily: 'var(--font-mono)',
  fontSize: 12,
};

// Severity → accent colour. The canonical store surfaces
// info/warning/error (plus debug/critical for completeness).
function severityColor(sev: string): string {
  switch (sev) {
    case 'critical':
    case 'error':
      return '#EF4444';
    case 'warning':
      return '#F59E0B';
    case 'debug':
      return '#737373';
    default:
      return '#22C55E'; // info
  }
}

// Compact "3m ago" relative label. Absolute timestamp rides the row's
// title attribute for hover.
function relativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) return iso;
  const diff = Date.now() - then;
  const sec = Math.round(diff / 1000);
  if (sec < 0) return 'just now';
  if (sec < 60) return `${sec}s ago`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const days = Math.round(hr / 24);
  return `${days}d ago`;
}

export function Events({ addToast }: Props) {
  const [events, setEvents] = useState<EventRecord[]>([]);
  const [cameras, setCameras] = useState<Camera[]>([]);
  const [eventTypes, setEventTypes] = useState<EventType[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Filters
  const [cameraId, setCameraId] = useState('');
  const [typeId, setTypeId] = useState('');
  const [unackOnly, setUnackOnly] = useState(false);

  // Per-event clip export state: download url once obtained, plus the
  // set of events with an export request in flight.
  const [clipUrls, setClipUrls] = useState<Record<string, string>>({});
  const [clipBusy, setClipBusy] = useState<Record<string, boolean>>({});

  const cameraName = useCallback(
    (id: string) => cameras.find((c) => c.id === id)?.name ?? id.slice(0, 8),
    [cameras],
  );
  const typeName = useCallback(
    (id: string) => eventTypes.find((t) => t.id === id)?.display_name ?? id,
    [eventTypes],
  );

  // Load the id→name reference data once.
  useEffect(() => {
    void fetchCameras(0, 200)
      .then((r) => setCameras(r.items))
      .catch(() => setCameras([]));
    void listEventTypes()
      .then(setEventTypes)
      .catch(() => setEventTypes([]));
  }, []);

  const refresh = useCallback(async () => {
    setError(null);
    try {
      const res = await listEvents({
        cameraId: cameraId || undefined,
        typeId: typeId || undefined,
        unacknowledged: unackOnly || undefined,
        itemsPerPage: 100,
      });
      setEvents(res.items);
    } catch (e) {
      setError((e as Error).message);
    }
    setLoading(false);
  }, [cameraId, typeId, unackOnly]);

  // Re-fetch whenever the filters change.
  useEffect(() => {
    setLoading(true);
    void refresh();
  }, [refresh]);

  // Live updates: any SSE message re-fetches the current page. Keep the
  // latest refresh in a ref so the subscription (mounted once) always
  // calls the filter-current fetch. On EventSource error we start a 30s
  // polling backstop.
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;
  useEffect(() => {
    let pollTimer: number | undefined;
    const startPoll = () => {
      if (pollTimer == null) {
        pollTimer = window.setInterval(() => void refreshRef.current(), 30_000);
      }
    };
    const unsub = subscribeEventsStream(
      {},
      () => void refreshRef.current(),
      () => startPoll(),
    );
    return () => {
      unsub();
      if (pollTimer != null) clearInterval(pollTimer);
    };
  }, []);

  async function doAck(ev: EventRecord) {
    try {
      await ackEvent(ev.id);
      addToast({ kind: 'success', title: 'EVENT ACKNOWLEDGED', icon: 'check-circle' });
      void refresh();
    } catch (e) {
      addToast({ kind: 'danger', title: 'ACK FAILED', body: (e as Error).message, icon: 'x' });
    }
  }

  async function doExportClip(ev: EventRecord) {
    setClipBusy((m) => ({ ...m, [ev.id]: true }));
    try {
      const res = await createEventClip(ev.id);
      setClipUrls((m) => ({ ...m, [ev.id]: res.download_url }));
      addToast({ kind: 'success', title: 'CLIP EXPORTED', body: 'Download ready', icon: 'download' });
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        addToast({
          kind: 'warning',
          title: 'FOOTAGE UNAVAILABLE',
          body: 'The footage for this event is no longer available.',
          icon: 'info',
        });
      } else {
        addToast({ kind: 'danger', title: 'CLIP EXPORT FAILED', body: (e as Error).message, icon: 'x' });
      }
    }
    setClipBusy((m) => ({ ...m, [ev.id]: false }));
  }

  return (
    <div
      style={{
        flex: 1,
        display: 'flex',
        flexDirection: 'column',
        minWidth: 0,
        background: 'var(--bg-primary)',
        overflow: 'auto',
      }}
    >
      <PageHeader
        breadcrumb="RECORDING SERVER / EVENTS"
        title="Events"
        sub="Camera events with snapshots and clip export"
        right={
          <Btn kind="ghost" size="sm" icon="refresh-cw" onClick={() => void refresh()}>
            Refresh
          </Btn>
        }
      />
      <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
        {/* Filters */}
        <div style={{ display: 'flex', gap: 16, alignItems: 'flex-end', flexWrap: 'wrap' }}>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', letterSpacing: 1 }}>
              CAMERA
            </span>
            <select value={cameraId} onChange={(e) => setCameraId(e.target.value)} style={selectStyle}>
              <option value="">All cameras</option>
              {cameras.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </label>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', letterSpacing: 1 }}>
              EVENT TYPE
            </span>
            <select value={typeId} onChange={(e) => setTypeId(e.target.value)} style={selectStyle}>
              <option value="">All types</option>
              {eventTypes.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.display_name}
                </option>
              ))}
            </select>
          </label>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', letterSpacing: 1 }}>
              UNACKNOWLEDGED
            </span>
            <Toggle on={unackOnly} onChange={setUnackOnly} label={unackOnly ? 'ONLY' : 'ALL'} />
          </div>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)', marginLeft: 'auto' }}>
            {error ?? (loading ? 'Loading events…' : `${events.length} events`)}
          </span>
        </div>

        <Card style={{ padding: 0 }}>
          <div style={{ padding: '12px 16px', borderBottom: '1px solid var(--border)' }}>
            <SectionHeader style={{ margin: 0 }}>EVENTS</SectionHeader>
          </div>
          {events.length === 0 && !loading && (
            <div
              style={{
                padding: 32,
                textAlign: 'center',
                fontFamily: 'var(--font-mono)',
                fontSize: 11,
                letterSpacing: 1,
                color: 'var(--text-muted)',
                textTransform: 'uppercase',
              }}
            >
              No events match the current filters
            </div>
          )}
          {events.map((ev) => (
            <EventRow
              key={ev.id}
              ev={ev}
              cameraName={cameraName(ev.camera_id)}
              typeLabel={typeName(ev.type_id)}
              clipUrl={clipUrls[ev.id]}
              clipBusy={!!clipBusy[ev.id]}
              onAck={() => void doAck(ev)}
              onExportClip={() => void doExportClip(ev)}
            />
          ))}
        </Card>
      </div>
    </div>
  );
}

function EventRow({
  ev,
  cameraName,
  typeLabel,
  clipUrl,
  clipBusy,
  onAck,
  onExportClip,
}: {
  ev: EventRecord;
  cameraName: string;
  typeLabel: string;
  clipUrl?: string;
  clipBusy: boolean;
  onAck: () => void;
  onExportClip: () => void;
}) {
  const acked = !!ev.acknowledged_at;
  const accent = severityColor(ev.severity);
  // Thumbnail click opens the full snapshot in a new tab (falls back to
  // the thumbnail url when no full snapshot is present).
  const openTarget = ev.snapshot_url || ev.thumbnail_url;

  return (
    <div
      style={{
        padding: '12px 16px',
        borderBottom: '1px solid var(--border)',
        display: 'grid',
        gridTemplateColumns: '64px 1fr auto',
        gap: 14,
        alignItems: 'center',
      }}
    >
      {/* Thumbnail / placeholder */}
      {ev.thumbnail_url ? (
        <img
          src={ev.thumbnail_url}
          alt=""
          onClick={() => openTarget && window.open(openTarget, '_blank', 'noopener')}
          style={{
            width: 64,
            height: 40,
            objectFit: 'cover',
            borderRadius: 3,
            border: '1px solid var(--border)',
            cursor: openTarget ? 'pointer' : 'default',
            display: 'block',
          }}
        />
      ) : (
        <div
          style={{
            width: 64,
            height: 40,
            borderRadius: 3,
            border: '1px solid var(--border)',
            background: 'var(--bg-tertiary)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          <Icon name="video" style={{ width: 14, height: 14, color: 'var(--text-muted)' }} />
        </div>
      )}

      {/* Body: severity dot + type + camera + time */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span
            style={{
              width: 6,
              height: 6,
              borderRadius: '50%',
              background: accent,
              boxShadow: `0 0 6px ${accent}`,
              flexShrink: 0,
            }}
          />
          <span style={{ fontFamily: 'var(--font-sans)', fontSize: 13, fontWeight: 500, color: 'var(--text-primary)' }}>
            {typeLabel}
          </span>
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 9,
              letterSpacing: 1,
              textTransform: 'uppercase',
              color: accent,
            }}
          >
            {ev.severity}
          </span>
          {acked && (
            <span
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 9,
                letterSpacing: 1,
                textTransform: 'uppercase',
                color: 'var(--text-muted)',
                display: 'inline-flex',
                alignItems: 'center',
                gap: 3,
              }}
              title={ev.acknowledged_by ? `by ${ev.acknowledged_by}` : undefined}
            >
              <Icon name="check" style={{ width: 10, height: 10 }} /> ACK
            </span>
          )}
        </div>
        <div
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 10,
            color: 'var(--text-muted)',
            display: 'flex',
            gap: 10,
          }}
        >
          <span>{cameraName}</span>
          <span title={new Date(ev.occurred_at).toLocaleString()}>{relativeTime(ev.occurred_at)}</span>
        </div>
      </div>

      {/* Actions */}
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', justifyContent: 'flex-end' }}>
        {!acked && (
          <Btn kind="ghost" size="sm" onClick={onAck}>
            Acknowledge
          </Btn>
        )}
        {clipUrl ? (
          <a
            href={clipUrl}
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 11,
              letterSpacing: 1,
              textTransform: 'uppercase',
              color: '#F97316',
              textDecoration: 'none',
              display: 'inline-flex',
              alignItems: 'center',
              gap: 5,
              padding: '0 10px',
              height: 26,
            }}
          >
            <Icon name="download" style={{ width: 12, height: 12 }} /> Download
          </a>
        ) : (
          <Btn kind="secondary" size="sm" icon="download" disabled={clipBusy} onClick={onExportClip}>
            {clipBusy ? 'Exporting…' : 'Export clip'}
          </Btn>
        )}
      </div>
    </div>
  );
}
