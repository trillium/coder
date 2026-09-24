# Pi-hole on lnx-server — runbook (bead task-ho49e)

Live state is the deliverable; this file is the reproducible record.
Deployed 2026-09-24. Host: `lnx-server` (Ubuntu 24.04, Docker 29.8.1),
Tailscale IP `100.81.88.113`, LAN IP `192.168.86.50` (DHCP).

## Decisions (with evidence)

- **DNS role: tailnet-reachable now, tailnet-wide via one admin-console click.**
  Pi-hole binds the stable Tailscale IP, so any tailnet node can use
  `100.81.88.113` as its resolver today (verified from MacBook).
  Making it the tailnet *default* requires a human click in the Tailscale
  admin console (DNS → Nameservers → add `100.81.88.113`, enable Override
  for `hippo-tilapia.ts.net`); left as an explicit opt-in, see below.
- **Adblock lists: Pi-hole defaults (StevenBlack unified + Pi-hole defaults).**
  No exotic lists; verified `doubleclick.net` and `ads.yahoo.com` → `0.0.0.0`
  while `example.com` resolves normally.
- **DHCP: DNS-only.** Taking over DHCP on `192.168.86.0/24` (router at
  `192.168.86.1`) would be disruptive with no demonstrated need. No port 67
  published.
- **Upstream DNS: Cloudflare `1.1.1.1;1.0.0.1`** via
  `FTLCONF_dns_upstreams`. Host itself keeps `192.168.86.1` via
  systemd-resolved (unchanged).
- **Admin password: random 20-char alphanumeric**, generated on lnx into
  `/root/pihole-admin-password` and `/root/pihole.env` (both `root:root`
  `0600`). Never printed, never committed. Rotate with:
  `sudo bash -c '... > /root/pihole-admin-password ...'` then recreate.
- **Host resolver untouched.** `0.0.0.0:53` cannot bind because
  systemd-resolved owns `127.0.0.53:53` + `127.0.0.54:53`, so DNS ports bind
  to explicit host IPs instead of disabling the stub listener (less invasive,
  zero host network changes).
- **Web UI on host port 8081 → container 80** (7080 coder, 8080 localhost
  CRM, 8181 lnx-viz, 8980 panel all avoided). Admin UI: `http://100.81.88.113:8081/admin/`.
- **Host Tailscale: `--accept-dns=false`** (`sudo tailscale set
  --accept-dns=false`) so the host never points at itself/Pi-hole in a loop.

## Exact image

- `pihole/pihole:2026.09.0` (FTL v6.7.1)
- Digest: `sha256:5b9c8cf51de7d6d3f2240dbe72baf5f06e1fd39cb4d77a99fab4fa13e23bd1be`

## Recreate command (password stays in root-only `/root/pihole.env`)

```sh
docker rm -f pihole
sudo docker run -d --name pihole --restart unless-stopped \
  --network bridge --cap-add NET_ADMIN \
  -p 100.81.88.113:53:53/tcp -p 100.81.88.113:53:53/udp \
  -p 192.168.86.50:53:53/tcp   -p 192.168.86.50:53:53/udp \
  -p 127.0.0.1:53:53/tcp       -p 127.0.0.1:53:53/udp \
  -p 8081:80/tcp \
  -e TZ=Etc/UTC \
  -e 'FTLCONF_dns_upstreams=1.1.1.1;1.0.0.1' \
  -e FTLCONF_dns_listeningMode=all \
  --env-file /root/pihole.env \
  -v /opt/pihole/etc-pihole:/etc/pihole \
  -v /opt/pihole/etc-dnsmasq.d:/etc/dnsmasq.d \
  pihole/pihole:2026.09.0
```

State: `/opt/pihole/etc-pihole` + `/opt/pihole/etc-dnsmasq.d` (bind mounts).
A full `stop`/`rm`/recreate was tested: same `gravity.db` (4.5 MB) reused,
blocking intact immediately after restart.

## Verification evidence (2026-09-24)

- From MacBook over tailnet: `dig @100.81.88.113 doubleclick.net` → `0.0.0.0`
  vs default resolver → `142.251.210.142`; `ads.yahoo.com` → `0.0.0.0` vs
  real `yahoodns` records; `example.com` → real IPs.
- From lnx via `100.81.88.113`, `192.168.86.50`, `127.0.0.1`: same
  blocked/unblocked behavior.
- Admin UI `302` (login redirect) from lnx loopback and from MacBook.
- Container `Up`, restart policy `unless-stopped`, on the existing `bridge`
  network (`172.17.0.5/16`).
- No shell access to other tailnet nodes (minis/hass/vps01) exists from this
  environment, so the second-client tailnet-shell test was impossible; the
  MacBook result plus a same-query control through the default resolver is
  the demonstrated live filtering.

## Remaining manual step (human, Tailscale admin console)

To make Pi-hole the tailnet-wide default resolver: DNS → Nameservers → add
`100.81.88.113` as a custom nameserver and enable **Override** for the
tailnet. Until then, per-device opt-in: set any tailnet node's DNS to
`100.81.88.113`.

## Maintenance caveats

- If the LAN DHCP lease changes `192.168.86.50`, recreate the container with
  the new LAN IP (the Tailscale IP binding is stable and is the primary path).
- IPv4 only; the host's Tailscale IPv6 address is not bound.
