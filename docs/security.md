# Security and Remote Access

StatLite uses deployment-level access controls: its native demo and generated
configurations bind to `127.0.0.1:9090`, keeping the listener off external
network interfaces. Custom configurations must explicitly set `server.listen`;
check that value before starting a deployment. Docker has a separate port
publishing boundary, described below.

## Local deployments and trust boundary

On a trusted machine, loopback access can be appropriate without an additional
authentication layer. StatLite does not implement dashboard or API login;
any client that can reach its listener can read the exposed operational data.
Treat target names, application health, metrics, and history as potentially
sensitive.

Loopback binding reduces network exposure. It does not isolate StatLite from
other local users or processes, compromised software, or requests relayed by
another service on the host. On a shared or untrusted host, use additional
access controls or an isolated deployment appropriate to that environment.
Restrict filesystem access to configuration, target credentials, and the SQLite
database as well.

## Remote access

Choose an access boundary that admits only intended users:

- **SSH tunnel:** keep StatLite on loopback and forward a local port through SSH.
- **VPN:** restrict access to authorized VPN peers using VPN access rules and
  host firewall rules. If clients connect directly, bind StatLite to the VPN
  address and ensure it is unreachable through public interfaces.
- **Authenticating HTTPS reverse proxy:** keep StatLite private behind the
  proxy and require authentication for every request.

For an SSH tunnel, replace `user@server` with your SSH destination:

```sh
ssh -N -o ExitOnForwardFailure=yes \
  -L 127.0.0.1:19090:127.0.0.1:9090 user@server
```

Keep the tunnel running and open <http://127.0.0.1:19090> on your machine.
The remote StatLite listener stays at `127.0.0.1:9090`.

## HTTPS and Basic Auth with Caddy

This example runs Caddy 2.8 or newer and StatLite on the same host. Keep
StatLite configured with:

```yaml
server:
  listen: "127.0.0.1:9090"
```

Use a domain you control, point its DNS records at the proxy host, and allow
inbound ports 80 and 443 to Caddy. Caddy needs outbound access to obtain
certificates and writable, persistent certificate storage. Keep port 9090
private. See Caddy's [automatic HTTPS requirements](https://caddyserver.com/docs/automatic-https).

Generate a password hash interactively so the password is not included in
shell history:

```sh
caddy hash-password
```

In `/etc/caddy/Caddyfile`, replace `statlite.example.com` with your domain and
`REPLACE_WITH_GENERATED_HASH` with the complete output hash. Choose your own
strong password; `operator` is the login username:

```caddyfile
statlite.example.com {
    basic_auth {
        operator REPLACE_WITH_GENERATED_HASH
    }
    reverse_proxy 127.0.0.1:9090
}
```

Validate and reload an installed Caddy systemd service:

```sh
sudo caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
sudo systemctl reload caddy
```

Open your `https://` URL and sign in. Caddy obtains and renews a certificate
and redirects HTTP to HTTPS. Always use HTTPS when sending Basic Auth
credentials. See the [Caddy Basic Auth reference](https://caddyserver.com/docs/caddyfile/directives/basic_auth).

### Protect the whole listener

The example applies authentication to every proxied path, covering the
dashboard, assets, `/api/*` (including `/api/v1/*`), `/statlite/metrics`, and
`/healthz`. Protecting only the dashboard HTML leaves operational data exposed.
API clients must use the same VPN, tunnel, or proxy authentication as browsers.
Local self-monitoring can continue to use the private loopback listener.

Do not expose a second listener or published port that bypasses the proxy.
From an external client, verify that requests without credentials to both `/`
and `/api/v1/status` return `401`, that valid credentials allow access, and
that direct connections to port 9090 are unavailable.

## Docker deployments

The bundled image listens on `0.0.0.0:9090` **inside the container** so Docker
can forward traffic to it. Restrict the host publication explicitly:

```sh
docker run --rm \
  -p 127.0.0.1:9090:9090 \
  docker.io/pvrlabs/statlite:latest
```

Publishing with `-p 9090:9090` binds all host interfaces by default and may
expose StatLite directly to public networks. Do not rely on the container's
internal address or a host firewall assumption as the access boundary.
On Linux, Docker-published ports can bypass UFW-style host firewall rules
because traffic is diverted before UFW's filtering, as explained in
[Docker's firewall documentation](https://docs.docker.com/engine/network/packet-filtering-firewalls/#docker-and-ufw).
Check Docker's port publishing and the actual reachable interfaces.

A Caddy process running on the host can proxy to the loopback-published port
using the configuration above. If Caddy runs in a separate container,
`127.0.0.1` refers to that Caddy container. Connect both containers through a
dedicated Docker network, proxy to StatLite's service name on port 9090, and
omit StatLite's host port publication. Other containers on that network can
reach StatLite directly, so limit network membership to trusted services.
Publish only the proxy's required ports for remote access.

See [Docker](docker.md) for configuration mounts and persistent storage.

## Monitored application endpoints

The access boundary above protects StatLite. Monitored applications' health
and metrics endpoints need their own network restrictions or authentication.
Target-side [Basic Auth configuration](configuration.md#basic-auth), where
supported by the integration, supplies credentials for polling an application;
it does not authenticate users accessing StatLite.
