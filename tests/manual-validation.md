# Manual platform validation

Automated Go tests cover portable compatibility behavior. Before release, a
maintainer must record the date, macOS version, Fleet commit, sing-box version,
and result for each platform boundary below.

- [ ] Keychain: add two subscriptions and confirm distinct `fleet.subscription.<UUID>` accounts.
- [ ] Proxy: start, exercise an HTTPS request, inspect status, and stop.
- [ ] Proxy ownership: after proxy start, change one macOS proxy setting externally;
      stop Fleet and confirm the external setting is not overwritten.
- [ ] TUN: start with administrator authorization, exercise traffic, inspect status, and stop.
- [ ] TUN exclusivity: with an external full-tunnel active, confirm `fleet tun start`
      fails before sudo, state/config writes, process launch, or a new utun; restart the
      external tunnel and repeat. Its traffic, routes, DNS, and interface must remain unchanged.
- [ ] TUN auto-route gate: without another full-tunnel, record `route -n get default`
      and both `netstat -rn -f inet` / `inet6` tables before start. After Fleet becomes
      active, the original physical default gateway/netif must still exist and Fleet's
      owned utun must carry the default or paired `/1` routes. Stop and verify the baseline again.
- [ ] TUN cleanup: after stop, confirm the Fleet TUN interface, routes, and DNS
      changes are gone.
- [ ] sing-box: validate vmess, hysteria2, anytls, and trojan nodes with the supported real binary.
- [ ] Runtime recovery: repeat proxy and TUN start with port occupation, denied
      sudo, interrupted startup, and a killed data-plane process; confirm Fleet
      either rolls back fully or reports a versioned `degraded` state without
      killing an unrelated PID.
- [ ] Port preflight: as root, occupy only TCP, only UDP, and then both protocols on
      Fleet's mixed port. A normal-user start must report the matching conflict before
      persistent or privileged side effects; releasing the sockets must permit start.
- [ ] Legacy state: exercise a running legacy `state.json`, an exited legacy
      process, and PID reuse; only the strictly matching sing-box process may be
      stopped or migrated.
- [ ] Health isolation: compare the persistent PID, system proxy, TUN, routes,
      and DNS before and after `fleet health`; none may change.
- [ ] Fresh install: run `scripts/install-local.sh`, then `fleet help`.
- [ ] Go rollback: install over an earlier Go binary and exercise the printed rollback command.
- [ ] Python retirement: install over a Python-shebang `fleet`; confirm it is archived and no rollback command restores it.

These checks are intentionally not marked complete by automated Linux runs.
