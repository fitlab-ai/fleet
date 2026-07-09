# Fleet

Fleet is a lightweight macOS proxy and TUN switcher powered by
[sing-box](https://sing-box.sagernet.org/).

It imports nodes from a Clash-compatible configuration, generates sing-box
configs, and starts either a local mixed HTTP/SOCKS proxy or a system-wide TUN
route.

## Requirements

- macOS
- Python 3
- Ruby with the standard `yaml` library
- sing-box installed at `/opt/homebrew/bin/sing-box`

## Install

```sh
install -m 0755 bin/fleet ~/.local/bin/fleet
```

Make sure `~/.local/bin` is in your `PATH`.

## Usage

```sh
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

By default, `fleet refresh` reads the Clash configuration from:

```text
~/Library/Application Support/com.follow.clash/config.yaml
```

The listening port defaults to `7890` and can be overridden:

```sh
FLEET_PORT=7891 fleet proxy start 0
```
