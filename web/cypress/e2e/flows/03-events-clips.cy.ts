// Flow: events → snapshots → signed media → event-to-clip (SP3/SP4).
describe('flow: events, snapshots, clips', () => {
  let token: string;
  beforeEach(() => {
    cy.apiToken().then((t) => (token = t));
  });

  it('lists real vendor events with signed snapshot URLs and filters by camera', () => {
    cy.api('GET', '/v1/events?items_per_page=50', token).then((r) => {
      expect(r.status).to.eq(200);
      const items = (r.body as { items: Array<{ id: string; camera_id: string; type_id: string; snapshot_url?: string }> }).items;
      expect(items.length, 'events exist from vendor channels').to.be.greaterThan(0);
      const withSnap = items.find((e) => e.snapshot_url);
      expect(withSnap, 'at least one event has a snapshot').to.exist;

      // Camera filter narrows the set.
      const cam = items[0].camera_id;
      cy.api('GET', `/v1/events?camera_id=${cam}&items_per_page=50`, token).then((fr) => {
        const filtered = (fr.body as { items: Array<{ camera_id: string }> }).items;
        filtered.forEach((e) => expect(e.camera_id).to.eq(cam));
      });
    });
  });

  it('signed snapshot URL serves the JPEG; a tampered signature is 403', () => {
    cy.api('GET', '/v1/events?items_per_page=50', token).then((r) => {
      const withSnap = (r.body as { items: Array<{ id: string; snapshot_url?: string }> }).items.find(
        (e) => e.snapshot_url,
      );
      expect(withSnap, 'event with snapshot').to.exist;
      const url = withSnap!.snapshot_url!;

      // Signed URL is anonymous-with-signature — no auth header needed.
      cy.request({ url, encoding: 'binary' }).then((sr) => {
        expect(sr.status).to.eq(200);
        // JPEG magic bytes.
        const bytes = sr.body as string;
        expect(bytes.charCodeAt(0)).to.eq(0xff);
        expect(bytes.charCodeAt(1)).to.eq(0xd8);
      });

      // Tamper the sig → 403.
      const tampered = url.replace(/sig=[0-9a-f]+/, 'sig=deadbeef');
      cy.request({ url: tampered, failOnStatusCode: false }).then((tr) => {
        expect(tr.status).to.eq(403);
      });
    });
  });

  it('exports a clip from an event, idempotently, and the signed clip downloads as mp4', () => {
    let eventId = '';
    let clipId = '';
    let downloadUrl = '';

    cy.api('GET', '/v1/events?type_id=motion&items_per_page=1', token).then((r) => {
      eventId = (r.body as { items: Array<{ id: string }> }).items[0].id;
    });

    cy.then(() => cy.api('POST', `/v1/events/${eventId}/clip`, token, {})).then((r) => {
      expect(r.status, 'create clip').to.be.oneOf([200, 201]);
      const body = r.body as { clip: { id: string }; download_url: string };
      clipId = body.clip.id;
      downloadUrl = body.download_url;
      expect(downloadUrl).to.contain('/v1/media/clips/');
    });

    // Idempotent: a second POST returns the same clip.
    cy.then(() => cy.api('POST', `/v1/events/${eventId}/clip`, token, {})).then((r) => {
      expect((r.body as { clip: { id: string } }).clip.id).to.eq(clipId);
    });

    // The preparer is async; poll the clip until ready, then download.
    const waitReady = (attempt = 0): void => {
      cy.api('GET', `/v1/clips/${clipId}`, token).then((r) => {
        const state = (r.body as { state: string }).state;
        if (state === 'ready') return;
        if (state === 'failed') throw new Error('clip preparation failed');
        if (attempt > 30) throw new Error(`clip stuck in ${state}`);
        cy.wait(500);
        waitReady(attempt + 1);
      });
    };
    cy.then(() => waitReady());

    cy.then(() =>
      cy.request({ url: downloadUrl, encoding: 'binary' }).then((dr) => {
        expect(dr.status).to.eq(200);
        const b = dr.body as string;
        // mp4 'ftyp' box at offset 4.
        expect(b.substring(4, 8)).to.eq('ftyp');
      }),
    );
  });

  it('acknowledges an event', () => {
    cy.api('GET', '/v1/events?unacknowledged=true&items_per_page=1', token).then((r) => {
      const items = (r.body as { items: Array<{ id: string }> }).items;
      if (items.length === 0) return; // all acked already — fine
      const id = items[0].id;
      cy.api('POST', `/v1/events/${id}/acknowledge`, token, {}).then((ar) => {
        expect(ar.status).to.be.oneOf([200, 204]);
      });
    });
  });
});
