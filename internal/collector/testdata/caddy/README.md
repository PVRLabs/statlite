# Recorded Caddy fixtures

Unmodified Caddy 2.11.4 captures from the 2026-10-22 spike, recorded with
Python 3.14.6 on macOS 15.6.1 amd64. The HTTPS fixture uses locally trusted
certificates, one named host, and one direct reverse proxy.

- `https-traffic.prom`: HTTPS traffic capture with one each of 200, 404, 400,
  500, and Caddy-generated 502, plus an automatic HTTP redirect.
- `https-after-monitoring.prom`: same application population after three
  scrapes of each monitoring endpoint, including the read-only listener.
- `two-hosts.prom`: named-site HTTP regression traffic capture with independent
  alpha.test and beta.test populations.

Full captures retain unrelated families to test selection and exclusion.
These fixtures test collection only; stored continuity and deployment
acceptance require the subsequent implementation phases.
