# Manual platform validation

Automated Go tests cover portable compatibility behavior. Before release, a
maintainer must record the date, macOS version, Fleet commit, sing-box version,
and result for each platform boundary below.

- [ ] Keychain: add two subscriptions and confirm distinct `fleet.subscription.<UUID>` accounts.
- [ ] Proxy: start, exercise an HTTPS request, inspect status, and stop.
- [ ] TUN: start with administrator authorization, exercise traffic, inspect status, and stop.
- [ ] sing-box: validate vmess, hysteria2, anytls, and trojan nodes with the supported real binary.
- [ ] Fresh install: run `scripts/install-local.sh`, then `fleet help`.
- [ ] Go rollback: install over an earlier Go binary and exercise the printed rollback command.
- [ ] Python retirement: install over a Python-shebang `fleet`; confirm it is archived and no rollback command restores it.

These checks are intentionally not marked complete by automated Linux runs.
