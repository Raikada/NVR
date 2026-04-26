# Security Policy

## Reporting a vulnerability

> **TBD: reporting address not yet configured.** Until this is filled
> in, please report suspected vulnerabilities through a private GitHub
> Security Advisory on this repository.

If you have found a security issue in the Raikada Recording Server,
**please do not open a public issue or pull request.** Disclose it
privately so we can investigate and ship a fix before details become
public.

We aim to acknowledge a report within two business days and to provide
a status update within ten business days.

## Scope

This policy covers the Raikada Recording Server in this repository.
Vulnerabilities in upstream MediaMTX are best reported to the
[MediaMTX project](https://github.com/bluenviron/mediamtx) directly;
we will mirror fixes here as they land.

Vulnerabilities in other Raikada platform components (Cloud, Management
Server, Web Client, Flutter Client) should be reported to the owning
repo. The platform's overall trust model is documented in
[`../platform/docs/trust-model.md`](../platform/docs/trust-model.md).

## Trust boundaries (summary)

The recorder operates inside a layered trust model documented in full
in [`../platform/docs/trust-model.md`](../platform/docs/trust-model.md). At a glance:

- **Cloud** is the trust root for cloud-connected deployments.
- **The Management Server** is the site trust authority for Recording
  Servers — this recorder trusts its paired MS for desired
  configuration and service credentials.
- **Cameras, web/Flutter clients, and broker-tunnelled connections**
  are untrusted until they present a valid token validated locally.
- **Break-glass paths** require physical or out-of-band access; they
  are loudly audited and tightly scoped.

Issues that effectively cross any of these boundaries — bypassing
authentication, escalating from a camera or LAN client into recorder
control, exfiltrating recordings to an unauthorized destination,
disabling audit, breaking the offline-first invariant — are the
highest priority.
