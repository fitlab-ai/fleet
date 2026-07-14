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
- sing-box installed at `/opt/homebrew/bin/sing-box`
- macOS Keychain (`/usr/bin/security`) for subscription credentials

## Install

```sh
install -m 0755 bin/fleet ~/.local/bin/fleet
```

Make sure `~/.local/bin` is in your `PATH`.

## Usage

```sh
fleet subscription add 'https://provider.example/subscription'
fleet subscription status
fleet refresh
fleet list
fleet ping

fleet proxy start <name|index>
fleet proxy stop
fleet proxy switch <name|index>

fleet tun start <name|index>
fleet tun stop
fleet tun switch <name|index>

fleet status
fleet stop
```

To keep a credential out of shell history, omit the URL and provide it on
standard input. Fleet accepts HTTPS URLs only and never prints the stored URL:

```sh
printf '%s\n' "$SUBSCRIPTION_URL" | fleet subscription add
```

Migrate an existing FlClash snapshot with its URL when the URL cannot be
recovered from the file:

```sh
fleet subscription migrate --url 'https://provider.example/subscription'
```

The current migration check expects 44 nodes: 29 VMess, 4 Hysteria2, and 11
AnyTLS. This distribution is not enforced for later refreshes.

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

The subscription URL is stored in macOS Keychain. Downloaded YAML and node
caches are stored in `0700` directories and `0600` files. Successful refreshes
are published as immutable generations under `~/.config/fleet/generations/`;
an atomically replaced `current.json` selects the active generation. Readers
verify manifest hashes and fall back to the previous valid generation, then a
legacy `nodes.json`, if the active cache is damaged.

`fleet refresh` identifies itself as `clash.meta` so subscription services can
return the supported Clash YAML format. Fleet does not decode Base64 URI lists
or other subscription formats. A non-Clash top-level response fails with the
safe `format` category; the response body, subscription URL, tokens, node names,
servers, and credentials are not included in the error or refresh state.

Refresh validates the HTTPS response, strict YAML structure, all node fields,
supported protocols (VMess, Hysteria2, AnyTLS), node counts, and every generated
proxy config with `sing-box check`. A refresh that shrinks below 50% of the
previous successful count is rejected unless `--force` is supplied. `--force`
does not bypass format or any other validation.

Refresh uses a 30-second request timeout. Set a positive integer override when
needed:

```sh
FLEET_SUBSCRIPTION_TIMEOUT=60 fleet refresh
```

TLS certificate and hostname verification always remain enabled; there is no
insecure mode. Refresh never starts, stops, or reloads sing-box and does not
change the current system proxy, Fleet runtime state, or TUN route. Failed
downloads, format negotiation, and validation leave the last usable cache
untouched. `subscription-state.json` records only the safe `last_error`
category, including `format` for unsupported top-level responses.

`fleet subscription remove` deletes the Keychain credential and refresh status
but intentionally retains cached nodes. FlClash is only consulted by the
explicit `subscription migrate` command and is not a refresh dependency.

The listening port defaults to `7890` and can be overridden:

```sh
FLEET_PORT=7891 fleet proxy start 0
```
