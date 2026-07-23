# Fleet

Fleet is a lightweight macOS proxy and TUN switcher powered by
[sing-box](https://sing-box.sagernet.org/).

It downloads nodes from a Fleet-managed Clash-compatible subscription,
validates them with sing-box, and starts either a local mixed HTTP/SOCKS proxy
or a system-wide TUN route.

## Requirements

- macOS
- Python 3
- Ruby with the standard `yaml` library
- stable sing-box 1.13.14 or newer installed at `/opt/homebrew/bin/sing-box`
- macOS Keychain (`/usr/bin/security`) for subscription credentials

## Install

```sh
install -m 0755 bin/fleet ~/.local/bin/fleet
```

Make sure `~/.local/bin` is in your `PATH`.

## Usage

```sh
fleet subscription add airport-a 'https://provider-a.example/subscription'
fleet subscription add airport-b 'https://provider-b.example/subscription'
fleet subscription status
fleet refresh
fleet list
fleet ping
fleet health

# Duplicate node names can be selected with their subscription source.
fleet proxy start '@airport-b/Hong Kong 01'

fleet proxy start <name|index>
fleet proxy stop
fleet proxy switch <name|index>

fleet tun start <name|index>
fleet tun stop
fleet tun switch <name|index>

fleet status
fleet stop
```

Names are case-insensitively unique and may contain letters, digits, `.`, `_`,
and `-`. The compatible one-argument form automatically assigns the smallest
available name (`subscription-1`, `subscription-2`, and so on) and prints its
name and short ID:

```sh
fleet subscription add 'https://provider.example/subscription'
```

To keep a credential out of shell history, provide a name but omit the URL, then
send the URL on standard input. Fleet accepts HTTPS URLs only and never prints
the stored URL:

```sh
printf '%s\n' "$SUBSCRIPTION_URL" | fleet subscription add airport-c
```

Migrate an existing FlClash snapshot with its URL when the URL cannot be
recovered from the file:

```sh
fleet subscription migrate --name legacy --url 'https://provider.example/subscription'
```

The current migration check expects 44 nodes: 29 VMess, 4 Hysteria2, 11
AnyTLS, and 0 Trojan. This distribution is not enforced for later refreshes.

## Modes

- `proxy`: starts a local mixed HTTP/SOCKS proxy on `127.0.0.1:7890` and sets
  the macOS system proxy.
- `tun`: starts a system-wide sing-box TUN route. The macOS system proxy is left
  unchanged; `127.0.0.1:7890` is still available for apps that are explicitly
  configured to use a local proxy.

## Configuration

Fleet stores generated files under:

```text
~/.config/fleet/
```

Each subscription URL is stored separately in macOS Keychain under its stable
subscription UUID. The non-secret `subscriptions.json` registry contains only
UUIDs, display names, timestamps, and lifecycle state. Downloaded YAML and node
caches are stored in `0700` directories and `0600` files:

```text
~/.config/fleet/
  subscriptions.json
  refresh.lock
  writer.lock
  subscriptions/<uuid>/
    current.json
    state.json
    generations/<generation>/{source.yaml,nodes.json,manifest.json}
```

Successful refreshes are published as immutable per-subscription generations.
Readers verify manifest hashes and fall back to that subscription's previous
valid generation if its active cache is damaged. Fleet aggregates all active
and removal-pending caches at read time; every node retains its source UUID and
name. Index selection remains available, and a globally unique bare node name
still works. Duplicate names must use `@subscription-name/full node name`.

`fleet refresh` identifies itself as `clash.meta` so subscription services can
return the supported Clash YAML format. Fleet does not decode Base64 URI lists
or other subscription formats. A non-Clash top-level response fails with the
safe `format` category; the response body, subscription URL, tokens, node names,
servers, and credentials are not included in the error or refresh state.

Refresh validates the HTTPS response, strict YAML structure, all node fields,
supported protocols (VMess, Hysteria2, AnyTLS, Trojan), node counts, and every
generated proxy config with `sing-box check`. Hysteria2 maps port hopping
(`ports` and a fixed `hop-interval`), Salamander obfuscation and password, ALPN,
bandwidth, and TLS settings supported by stable sing-box 1.13.14.
Connection-affecting options that Fleet cannot safely translate, including
`mport`, randomized hop intervals, Gecko obfuscation, BBR profiles, certificate
fingerprints, Realm options, and QUIC tuning fields, are rejected instead of
silently ignored. Trojan support covers the basic
TCP + TLS form with password, optional SNI, a boolean `skip-cert-verify`, and an
optional ALPN string list. Trojan WebSocket, gRPC, and other transports are
rejected instead of being silently ignored. A refresh that shrinks below 50% of
that subscription's previous successful count is rejected unless `--force` is
supplied. `--force` does not bypass format or any other validation.
`fleet refresh NAME` refreshes one subscription; `fleet refresh` processes every
active subscription and continues after an individual failure. Successful
subscriptions publish normally, failed ones keep their last usable cache, and
the command exits non-zero if any failed.

Refresh uses a 30-second request timeout. Set a positive integer override when
needed:

```sh
FLEET_SUBSCRIPTION_TIMEOUT=60 fleet refresh
```

`fleet ping [node]` is only a direct TCP connect test to the advertised server
port. It reports `REACHABLE` or `UNREACHABLE`; it does not perform a proxy
protocol handshake and is not meaningful as Hysteria2/QUIC health.

`fleet health [node]` starts an isolated temporary sing-box instance for each
selected node and makes an HTTPS request through its mixed proxy. It does not
change Fleet's running instance, selected node, system proxy, or TUN state.
The default target is `https://api.github.com`; override it with a
credential-free HTTPS URL in `FLEET_HEALTH_URL`, and set the bounded request
timeout with `FLEET_HEALTH_TIMEOUT`.

TLS certificate and hostname verification are enabled by default, and Fleet has
no CLI option to bypass them. If a trusted subscription explicitly sets the
boolean `skip-cert-verify: true` for Hysteria2, AnyTLS, or Trojan, Fleet maps it
to sing-box `tls.insecure: true`; the subscription provider is therefore part of
the security boundary. Refresh never starts, stops, or reloads sing-box and does
not change the current system proxy, Fleet runtime state, or TUN route. Failed
downloads, format negotiation, and validation leave the last usable cache
untouched. Each subscription's `state.json` records only the safe `last_error`
category, including `format` for unsupported top-level responses.

`fleet subscription remove NAME|ID` deletes only that Keychain credential and
marks its cached nodes as `removed` without stopping proxy/TUN runtime. Removal
cannot be undone. The next `fleet refresh` purges removal-pending registry and
cache data; after that, the name may be added again with a new UUID and URL.

All new write commands acquire `refresh.lock` followed by `writer.lock`. This
coordinates with older Fleet refresh/migrate processes during upgrade. Do not
mix old and new binaries for add/remove operations because older releases did
not lock those commands. The first new write migrates a legacy single
subscription into `subscription-1` without deleting the legacy Keychain item or
cache, so readers can safely fall back if migration does not commit. FlClash is
only consulted by the explicit `subscription migrate` command and is not a
refresh dependency.

The listening port defaults to `7890` and can be overridden:

```sh
FLEET_PORT=7891 fleet proxy start 0
```
