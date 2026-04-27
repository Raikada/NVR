package api //nolint:revive

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// helperOpenDiskBuffer opens a fresh AuditDiskBuffer in a tempdir and
// arranges for it to be Closed at test end.
func helperOpenDiskBuffer(t *testing.T, opts AuditDiskBufferOptions) *AuditDiskBuffer {
	t.Helper()
	if opts.Dir == "" {
		opts.Dir = t.TempDir()
	}
	b, err := OpenAuditDiskBuffer(opts)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = b.Close()
	})
	return b
}

// TestAuditDiskBuffer_RoundTripSurvivesRestart verifies entries
// written before a simulated restart are recovered intact, and the
// chain head is restored so a subsequent Append continues the chain.
func TestAuditDiskBuffer_RoundTripSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	// Pass 1: write three entries via a chain wired to the disk buf.
	b1, err := OpenAuditDiskBuffer(AuditDiskBufferOptions{Dir: dir})
	require.NoError(t, err)
	require.Equal(t, defs.ZeroPrevHash, b1.HeadHashOnDisk())

	chain := NewAuditChain(defs.AuditEmitterKindRecordingServer, "rs-1", nil)
	chain.replaceSink(b1, b1.HeadHashOnDisk())

	e1, err := chain.Append(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindServiceAccount,
		Action:       "config.applied",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "camera",
		ResourceID:   "cam-1",
	}, "tenant-1", "")
	require.NoError(t, err)
	e2, err := chain.Append(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindServiceAccount,
		Action:       "config.applied",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "camera",
		ResourceID:   "cam-2",
	}, "tenant-1", "")
	require.NoError(t, err)
	e3, err := chain.Append(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindServiceAccount,
		Action:       "auth.login",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "session",
	}, "tenant-1", "")
	require.NoError(t, err)
	require.NoError(t, b1.Close())

	// Pass 2: reopen, verify recovery.
	b2, err := OpenAuditDiskBuffer(AuditDiskBufferOptions{Dir: dir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = b2.Close() })

	require.Equal(t, 3, b2.Len())
	snap := b2.Snapshot()
	require.Equal(t, e1.ID, snap[0].ID)
	require.Equal(t, e2.ID, snap[1].ID)
	require.Equal(t, e3.ID, snap[2].ID)
	require.Equal(t, e3.EntryHash, b2.HeadHashOnDisk(),
		"head hash must equal the last entry's entry_hash post-restart")

	// And a follow-up Append on a chain pointed at the recovered
	// buffer must link to e3, not zero.
	chain2 := NewAuditChain(defs.AuditEmitterKindRecordingServer, "rs-1", nil)
	chain2.replaceSink(b2, b2.HeadHashOnDisk())
	e4, err := chain2.Append(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindServiceAccount,
		Action:       "auth.login",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "session",
	}, "tenant-1", "")
	require.NoError(t, err)
	require.Equal(t, e3.EntryHash, e4.PrevHash,
		"chain must continue across restart: e4.prev_hash == e3.entry_hash")
}

// TestAuditDiskBuffer_DetectsCorruptEntryHash verifies that
// hand-editing an entry_hash to be wrong causes Open to return
// ErrAuditChainCorrupt rather than silently continuing.
func TestAuditDiskBuffer_DetectsCorruptEntryHash(t *testing.T) {
	dir := t.TempDir()

	b, err := OpenAuditDiskBuffer(AuditDiskBufferOptions{Dir: dir})
	require.NoError(t, err)
	chain := NewAuditChain(defs.AuditEmitterKindRecordingServer, "rs-1", nil)
	chain.replaceSink(b, b.HeadHashOnDisk())
	_, err = chain.Append(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindServiceAccount,
		Action:       "config.applied",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "camera",
	}, "tenant-1", "")
	require.NoError(t, err)
	require.NoError(t, b.Close())

	// Corrupt: read the file, replace one of the entry_hash hex
	// characters, write back.
	logPath := filepath.Join(dir, auditDiskLogFilename)
	raw, err := os.ReadFile(logPath)
	require.NoError(t, err)

	// The payload begins after a 4-byte length prefix. Find an
	// entry_hash hex character in the payload and flip it.
	idx := strings.Index(string(raw[4:]), `"entry_hash":"`)
	require.GreaterOrEqual(t, idx, 0, "entry_hash field must be present in canonical-JSON")
	flipPos := 4 + idx + len(`"entry_hash":"`)
	if raw[flipPos] == 'a' {
		raw[flipPos] = 'b'
	} else {
		raw[flipPos] = 'a'
	}
	require.NoError(t, os.WriteFile(logPath, raw, 0o600))

	// Open: must fail loudly with ErrAuditChainCorrupt.
	_, err = OpenAuditDiskBuffer(AuditDiskBufferOptions{Dir: dir})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAuditChainCorrupt)
}

// TestAuditDiskBuffer_DetectsBrokenPrevHash verifies a manual
// prev_hash break is caught as ErrAuditChainCorrupt.
func TestAuditDiskBuffer_DetectsBrokenPrevHash(t *testing.T) {
	dir := t.TempDir()

	b, err := OpenAuditDiskBuffer(AuditDiskBufferOptions{Dir: dir})
	require.NoError(t, err)
	chain := NewAuditChain(defs.AuditEmitterKindRecordingServer, "rs-1", nil)
	chain.replaceSink(b, b.HeadHashOnDisk())
	for i := 0; i < 2; i++ {
		_, err := chain.Append(defs.AuditLogEntryInput{
			ActorKind:    defs.AuditActorKindServiceAccount,
			Action:       "config.applied",
			Outcome:      defs.AuditOutcomeSuccess,
			ResourceKind: "camera",
		}, "tenant-1", "")
		require.NoError(t, err)
	}
	require.NoError(t, b.Close())

	logPath := filepath.Join(dir, auditDiskLogFilename)
	raw, err := os.ReadFile(logPath)
	require.NoError(t, err)

	// Find the *second* entry's prev_hash and flip a character. The
	// canonical-JSON is sorted lex so prev_hash precedes entry_hash;
	// we need the second occurrence.
	first := strings.Index(string(raw), `"prev_hash":"`)
	require.GreaterOrEqual(t, first, 0)
	rest := raw[first+len(`"prev_hash":"`):]
	second := strings.Index(string(rest), `"prev_hash":"`)
	require.GreaterOrEqual(t, second, 0, "two prev_hash fields must be present")
	flipPos := first + len(`"prev_hash":"`) + second + len(`"prev_hash":"`)
	if raw[flipPos] == 'a' {
		raw[flipPos] = 'b'
	} else {
		raw[flipPos] = 'a'
	}
	require.NoError(t, os.WriteFile(logPath, raw, 0o600))

	_, err = OpenAuditDiskBuffer(AuditDiskBufferOptions{Dir: dir})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAuditChainCorrupt)
}

// TestAuditDiskBuffer_TornTailRecovery verifies a partial trailing
// record (e.g. process killed mid-write between length-prefix and
// payload) is silently truncated rather than treated as corruption.
// This is the one recovery path that is NOT a chain integrity
// failure: nothing was acknowledged to a producer because Append's
// fsync follows the full record.
func TestAuditDiskBuffer_TornTailRecovery(t *testing.T) {
	dir := t.TempDir()

	b, err := OpenAuditDiskBuffer(AuditDiskBufferOptions{Dir: dir})
	require.NoError(t, err)
	chain := NewAuditChain(defs.AuditEmitterKindRecordingServer, "rs-1", nil)
	chain.replaceSink(b, b.HeadHashOnDisk())
	_, err = chain.Append(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindServiceAccount,
		Action:       "config.applied",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "camera",
	}, "tenant-1", "")
	require.NoError(t, err)
	require.NoError(t, b.Close())

	// Append a partial record: just a length prefix, no payload.
	logPath := filepath.Join(dir, auditDiskLogFilename)
	f, err := os.OpenFile(logPath, os.O_RDWR|os.O_APPEND, 0o600)
	require.NoError(t, err)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], 200) // claims 200 bytes; absent
	_, err = f.Write(lenBuf[:])
	require.NoError(t, err)
	require.NoError(t, f.Close())

	b2, err := OpenAuditDiskBuffer(AuditDiskBufferOptions{Dir: dir})
	require.NoError(t, err, "torn tail must be silently truncated, not flagged corrupt")
	t.Cleanup(func() { _ = b2.Close() })
	require.Equal(t, 1, b2.Len())
}

// TestAuditDiskBuffer_Compaction_DropsOldEntries verifies that
// records with RecordedAt past the retention horizon are dropped on
// Compact, the in-memory ring is rebuilt from the survivors, and the
// chain head is preserved for surviving-tail-or-empty.
func TestAuditDiskBuffer_Compaction_DropsOldEntries(t *testing.T) {
	dir := t.TempDir()
	b, err := OpenAuditDiskBuffer(AuditDiskBufferOptions{
		Dir:           dir,
		RetentionDays: 7,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })

	chain := NewAuditChain(defs.AuditEmitterKindRecordingServer, "rs-1", nil)
	chain.replaceSink(b, b.HeadHashOnDisk())

	// Append three "old" entries (RecordedAt = now-30d) and two
	// "fresh" entries (RecordedAt = now). Inputs.OccurredAt drives
	// OccurredAt; the chain stamps RecordedAt = time.Now(). To force
	// older RecordedAt we Append directly into the disk buffer with
	// hand-stamped entries.
	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour)
	prev := defs.ZeroPrevHash
	var fourthHash, fifthHash string
	for i := 0; i < 5; i++ {
		recAt := old
		if i >= 3 {
			recAt = now
		}
		entry := defs.AuditLogEntry{
			ID:           "id-" + string(rune('A'+i)),
			TenantID:     "tenant-1",
			OccurredAt:   recAt,
			RecordedAt:   recAt,
			EmitterKind:  defs.AuditEmitterKindRecordingServer,
			EmitterID:    "rs-1",
			ActorKind:    defs.AuditActorKindServiceAccount,
			Action:       "config.applied",
			Outcome:      defs.AuditOutcomeSuccess,
			ResourceKind: "camera",
			PrevHash:     prev,
		}
		h, err := defs.ComputeAuditEntryHash(entry)
		require.NoError(t, err)
		entry.EntryHash = h
		b.Append(entry)
		prev = h
		if i == 3 {
			fourthHash = h
		}
		if i == 4 {
			fifthHash = h
		}
	}
	require.Equal(t, 5, b.Len())

	// Compact with cutoff at now-7d: the three old entries drop, the
	// two fresh ones stay.
	require.NoError(t, b.Compact(now))

	require.Equal(t, 2, b.Len(), "two fresh entries must survive compaction")
	snap := b.Snapshot()
	require.Equal(t, fourthHash, snap[0].EntryHash)
	require.Equal(t, fifthHash, snap[1].EntryHash)
	require.Equal(t, fifthHash, b.HeadHashOnDisk(),
		"chain head must equal the last surviving entry's hash")

	// Verify chain links among survivors still hold internally.
	require.Equal(t, snap[0].EntryHash, snap[1].PrevHash,
		"survivors' chain link must be intact post-compaction")

	// NOTE on post-compaction reopen: the surviving first entry's
	// prev_hash references a dropped predecessor (the last old
	// entry), so recover()'s "first prev_hash must equal
	// ZeroPrevHash" check would flag this as a chain break. That is
	// the documented limitation: post-compaction, the disk log is
	// internally chain-consistent but a fresh recovery cannot
	// distinguish a legitimate compacted-tail from a forged first
	// entry without a sealing-record (a "chain.compacted" entry
	// re-anchoring the chain to ZeroPrevHash). The sealing-record
	// design is queued as ADR 0006 follow-up work; this commit
	// implements the disk-backed buffer's primary contract
	// (durability across restart, corruption detection, retention
	// drop) and leaves compaction-with-recovery as a separate
	// concern. Compaction in production today is therefore safe
	// when the buffer stays open across the call (the in-memory
	// ring and disk handle are rebuilt internally); restart-after-
	// compaction needs the sealing-record layer.
}

// TestAuditDiskBuffer_Degraded_AtThreshold verifies IsDegraded flips
// to true when the on-disk log crosses 80% of AuditMaxDiskBytes.
func TestAuditDiskBuffer_Degraded_AtThreshold(t *testing.T) {
	dir := t.TempDir()

	// Tiny ceiling so we don't have to write thousands of entries.
	// 8 KiB ceiling -> 80% threshold = ~6.4 KiB. Each canonical-JSON
	// entry is on the order of 400 bytes; ~17 entries trips the
	// threshold.
	const cap8KiB = int64(8 * 1024)
	b, err := OpenAuditDiskBuffer(AuditDiskBufferOptions{
		Dir:          dir,
		MaxDiskBytes: cap8KiB,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })

	chain := NewAuditChain(defs.AuditEmitterKindRecordingServer, "rs-1", nil)
	chain.replaceSink(b, b.HeadHashOnDisk())

	require.False(t, b.IsDegraded(), "empty buffer is not degraded")

	// Append until the threshold trips. Cap iterations so a serializer
	// regression doesn't infinite-loop the test.
	tripped := false
	for i := 0; i < 200; i++ {
		_, err := chain.Append(defs.AuditLogEntryInput{
			ActorKind:    defs.AuditActorKindServiceAccount,
			Action:       "config.applied",
			Outcome:      defs.AuditOutcomeSuccess,
			ResourceKind: "camera",
			ResourceID:   "cam-deadbeef",
		}, "tenant-1", "")
		require.NoError(t, err)
		if b.IsDegraded() {
			tripped = true
			// Verify diskSize is at or above 80% of ceiling.
			require.GreaterOrEqual(t, b.DiskSize()*5, cap8KiB*4,
				"degraded must trip at >=80%% of max disk bytes")
			break
		}
	}
	require.True(t, tripped, "expected IsDegraded to flip true within bounded appends")
}

// TestAuditDiskBuffer_RecoveryEmptyFile verifies a freshly-created
// (empty) log file recovers cleanly with HeadHashOnDisk =
// ZeroPrevHash.
func TestAuditDiskBuffer_RecoveryEmptyFile(t *testing.T) {
	b := helperOpenDiskBuffer(t, AuditDiskBufferOptions{})
	require.Equal(t, defs.ZeroPrevHash, b.HeadHashOnDisk())
	require.Equal(t, 0, b.Len())
	require.False(t, b.IsDegraded())
}

// TestInstallAuditDiskSink_TransparentReadPath verifies that after
// installing a disk-backed sink, the read-side handlers
// (defaultAuditSink Snapshot, IsDegraded) observe the disk-backed
// buffer rather than the in-memory ring, and that uninstall reverts
// cleanly.
func TestInstallAuditDiskSink_TransparentReadPath(t *testing.T) {
	defaultAuditBuffer().Clear()
	defaultAuditChain().resetForTests()
	t.Cleanup(func() {
		uninstallAuditDiskSink()
		defaultAuditBuffer().Clear()
		defaultAuditChain().resetForTests()
	})

	dir := t.TempDir()
	b, err := OpenAuditDiskBuffer(AuditDiskBufferOptions{Dir: dir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })

	installAuditDiskSink(b)

	// Append through the default chain — must land in the disk buffer,
	// not the in-memory ring.
	cnf := tempConf(t, "api: yes\n")
	a := &API{Conf: cnf}
	a.emitAuthDecision(
		defs.AuditOutcomeSuccess,
		defs.AuditActorKindServiceAccount,
		"", "192.0.2.1", "", nil,
	)

	require.Equal(t, 0, defaultAuditBuffer().Len(),
		"in-memory ring must NOT receive entries while disk sink is installed")
	require.Equal(t, 1, b.Len(), "disk-backed buffer must receive the entry")
	require.Equal(t, 1, defaultAuditSink().Len(),
		"defaultAuditSink must observe the disk-backed buffer")

	// Reverse: uninstall and verify subsequent emits land in-memory.
	uninstallAuditDiskSink()
	a.emitAuthDecision(
		defs.AuditOutcomeSuccess,
		defs.AuditActorKindServiceAccount,
		"", "192.0.2.2", "", nil,
	)
	require.Equal(t, 1, defaultAuditBuffer().Len(),
		"after uninstall, in-memory ring receives new entries")
	require.Equal(t, 1, b.Len(), "disk-backed buffer's prior entry remains; no new ones")
}
