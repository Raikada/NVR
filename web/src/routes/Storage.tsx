// Storage route — disk overview, breakdown bar, RAID disk list with
// SMART. Faithful port of the design's StorageRoute.

import {
  Card,
  Progress,
  SectionHeader,
  Stat,
  StatusBadge,
} from '../components/primitives';
import { PageHeader } from '../components/PageHeader';

interface Disk {
  dev: string;
  model: string;
  used: number;
  total: number;
  status: 'online' | 'offline' | 'degraded';
  hours: number;
}

const DISKS: Disk[] = [
  { dev: '/dev/sda', model: 'Seagate Ironwolf 2TB', used: 256.4, total: 2000, status: 'online', hours: 4218 },
  { dev: '/dev/sdb', model: 'Seagate Ironwolf 2TB', used: 256.4, total: 2000, status: 'online', hours: 4218 },
];

export function Storage() {
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
        breadcrumb="RECORDING SERVER / STORAGE"
        title="Storage"
        sub="RAID1 mirror · 2 TB usable · 14-day retention"
      />
      <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 12 }}>
          <Stat
            label="USED"
            value="256.4"
            unit="GB"
            tone="accent"
            sub="12.8% of 2 TB"
            icon="pie-chart"
          />
          <Stat
            label="FREE"
            value="1,743.6"
            unit="GB"
            tone="success"
            sub="≈ 23 DAYS AT CURRENT RATE"
            icon="database"
          />
          <Stat
            label="WRITE RATE"
            value="18.4"
            unit="Mb/s"
            tone="accent"
            sub="6-CAMERA AGGREGATE"
            icon="arrow-down"
          />
          <Stat
            label="RETENTION"
            value="14"
            unit="DAYS"
            tone="accent"
            sub="ROLLING · INHERIT FROM MS"
            icon="rotate-ccw"
          />
        </div>
        <Card>
          <SectionHeader>STORAGE BREAKDOWN</SectionHeader>
          <div
            style={{
              height: 16,
              display: 'flex',
              borderRadius: 4,
              overflow: 'hidden',
              border: '1px solid var(--border)',
              marginTop: 8,
            }}
          >
            <div style={{ flex: 130, background: '#F97316' }} />
            <div style={{ flex: 82, background: '#EF4444' }} />
            <div style={{ flex: 44, background: '#EAB308' }} />
            <div style={{ flex: 1744, background: 'var(--bg-tertiary)' }} />
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 10, marginTop: 12 }}>
            <LegendItem color="#F97316" label="Continuous" value="130.2 GB" />
            <LegendItem color="#EF4444" label="Motion events" value="82.5 GB" />
            <LegendItem color="#EAB308" label="AI detections" value="43.7 GB" />
            <LegendItem color="var(--text-muted)" label="Free" value="1,743.6 GB" />
          </div>
        </Card>
        <Card style={{ padding: 0 }}>
          <div style={{ padding: '14px 16px', borderBottom: '1px solid var(--border)' }}>
            <SectionHeader style={{ margin: 0 }}>DISKS</SectionHeader>
          </div>
          {DISKS.map((d, i) => (
            <div
              key={d.dev}
              style={{
                padding: 14,
                borderTop: i === 0 ? 'none' : '1px solid var(--border)',
                display: 'grid',
                gridTemplateColumns: '120px 1fr 180px 100px',
                gap: 14,
                alignItems: 'center',
              }}
            >
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 13, color: '#F97316' }}>{d.dev}</span>
              <div>
                <div style={{ fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--text-primary)' }}>
                  {d.model}
                </div>
                <div
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 10,
                    color: 'var(--text-muted)',
                    marginTop: 3,
                    letterSpacing: 0.5,
                  }}
                >
                  POWER-ON {d.hours.toLocaleString()} H · SMART PASSED
                </div>
              </div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                <Progress value={(d.used / d.total) * 100} />
                <span
                  style={{
                    fontFamily: 'var(--font-mono)',
                    fontSize: 10,
                    color: 'var(--text-muted)',
                    letterSpacing: 0.5,
                  }}
                >
                  {d.used} GB / {d.total} GB
                </span>
              </div>
              <StatusBadge kind={d.status} />
            </div>
          ))}
        </Card>
      </div>
    </div>
  );
}

function LegendItem({ color, label, value }: { color: string; label: string; value: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
      <span style={{ width: 10, height: 10, borderRadius: 2, background: color }} />
      <div>
        <div
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 10,
            color: 'var(--text-muted)',
            letterSpacing: 1,
            textTransform: 'uppercase',
          }}
        >
          {label}
        </div>
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-primary)', marginTop: 2 }}>
          {value}
        </div>
      </div>
    </div>
  );
}
