# Running a daimon as a systemd service

This guide turns the manual `tmux + daimon init + daimon unlock` dance into a
boot-survivable two-unit setup. The result: after a server reboot (kernel
updates, panics, anything), the daimon comes back up automatically, unlocks
its keystore non-interactively, and starts accepting Noise IK connections.

Intended for **hosted / public daimons** — a VPS running as project
infrastructure, a relay box, a build that serves federated calls 24/7.
**Not** intended for laptops or personal daimons; the interactive
`daimon unlock` flow is the right pattern there (the password should live
in your head, not on disk).

## The two units

`daimond.service` runs the long-lived JSON-RPC daemon as the unprivileged
`daimon` user with full systemd hardening (NoNewPrivileges, ProtectSystem,
etc.) so a compromised daimond can't escalate or touch unrelated files.

`daimon-unlock.service` is a `Type=oneshot` that runs immediately after
daimond starts, sends `daimon.identity.unlock` over the local Unix socket
using the password read from a chmod-0600 file, and brings the public peer
listener up on TCP 0.0.0.0:9999.

The split keeps responsibilities clean: daimond owns the socket; the
unlock unit owns the auth + listener. If you ever want to change the bind
address or rotate the password, you only touch the unlock unit.

## One-time setup on the server

Assumes you already have a `daimon` user, a working `daimon init` (so a
keystore file exists), and the daimon CLI on PATH. If not, follow the
public-daimon recipe in the project README first.

```sh
# 1. Create a root-only directory for the password file
sudo mkdir -p /etc/daimon
sudo chown root:daimon /etc/daimon
sudo chmod 750 /etc/daimon

# 2. Write the keystore password into a file readable only by the daimon user
sudo install -m 0640 -o root -g daimon /dev/null /etc/daimon/keystore-password
sudo nano /etc/daimon/keystore-password
#   paste the password (the same one you typed at `daimon init`) — single line,
#   no trailing whitespace beyond the final newline. The CLI trims one trailing
#   newline so an `echo "..." > file` produces the correct bytes.
sudo chmod 0640 /etc/daimon/keystore-password
#   The 0640 above (root:daimon, daimon-readable) is the recommended chmod.
#   `daimon unlock --password-file` refuses anything more permissive than 0600
#   when the file is owned by the invoking user, and refuses world-readable
#   files entirely. Group-readable by `daimon` is the systemd-friendly way.

# 3. Copy the systemd units from the repo into the system location
sudo cp docs/systemd/daimond.service /etc/systemd/system/
sudo cp docs/systemd/daimon-unlock.service /etc/systemd/system/

# 4. Enable + start both (the unlock unit pulls in daimond automatically)
sudo systemctl daemon-reload
sudo systemctl enable --now daimond.service daimon-unlock.service

# 5. Verify both are happy
systemctl status daimond.service daimon-unlock.service
```

Expected status output (truncated):

```
● daimond.service - Daimon background daemon
   Loaded: loaded (/etc/systemd/system/daimond.service; enabled; …)
   Active: active (running) since Mon 2026-05-25 15:30:01 UTC; 12s ago
   Main PID: 12345 (daimond)
   …

● daimon-unlock.service - Daimon keystore unlock + peer listener
   Loaded: loaded (/etc/systemd/system/daimon-unlock.service; enabled; …)
   Active: active (exited) since Mon 2026-05-25 15:30:02 UTC; 11s ago
   …
```

If `daimon-unlock.service` shows `failed` instead, `journalctl -u daimon-unlock`
will show the exact error — most commonly:

- `--password-file: permissions 0o644; must be 0600 or 0400` → tighten the chmod
- `unlock failed: wrong password or corrupted keystore` → the file content
  doesn't match the keystore. Edit `/etc/daimon/keystore-password` and retry.
- `bind: address already in use` → another process holds 9999. Find it
  with `ss -tlnp | grep 9999`.

## Verifying reboot survival

```sh
# Reboot
sudo reboot

# Wait ~30 seconds, SSH back in, check:
systemctl is-active daimond.service daimon-unlock.service
# expected:
#   active
#   active

# And confirm the listener actually responded:
daimon federation config
# should print the same DID + endpoint as before the reboot.
```

If both units come up clean and `federation config` prints the listener,
the public daimon is reboot-survivable. Hetzner / Vultr / DigitalOcean
can reboot the host whenever they want (kernel updates, security patches,
hardware migrations) and the daimon comes back up on its own.

## Tailing logs

```sh
# Both units in one stream:
journalctl -u daimond -u daimon-unlock -f

# Just the daemon:
journalctl -u daimond -f

# Last 50 lines, ordered oldest-first:
journalctl -u daimond -n 50 --no-pager
```

The daimon's audit log (separate from systemd's stdout/stderr) lives under
`$DAIMON_HOME/activity.db` and is the canonical record of who dialed,
what they invoked, what was served. Read with `daimon activity query`.

## Rotating the password

```sh
# 1. Stop the unlock unit (daimond keeps running with the current unlock state)
sudo systemctl stop daimon-unlock.service

# 2. Change the at-rest password via the offline CLI
daimon rotate-password
#   prompts old + new passwords, rewrites the keystore

# 3. Update the systemd password file
sudo nano /etc/daimon/keystore-password
#   replace with the new password

# 4. Restart daimond + unlock
sudo systemctl restart daimond.service
sudo systemctl start daimon-unlock.service

# 5. Verify
systemctl status daimon-unlock.service
```

## Security notes

- The password file is the single most sensitive artifact in this setup.
  Anyone with read access to `/etc/daimon/keystore-password` can unlock
  the keystore, sign with the daimon's identity key, and drain any wallet
  the daimon holds. Keep it `chmod 0640 root:daimon`.
- The `daimon` user should NOT have a login shell unless you specifically
  use it for SSH. Add `usermod -s /usr/sbin/nologin daimon` after the SSH
  key copy if the user is service-account only.
- The systemd hardening directives in `daimond.service` (NoNewPrivileges,
  ProtectSystem, etc.) reduce the blast radius of a compromised daimond.
  They are conservative — if you need to relax them (e.g. to add a custom
  provider that talks to a Unix socket outside ReadWritePaths), do it
  deliberately with a comment explaining why.
- Backups: snapshot `$DAIMON_HOME` (`/home/daimon/.config/daimon` by
  default) regularly. `daimon backup --to /path/to/file.dbk` produces
  a single encrypted file safe to copy off-host. See `daimon backup --help`.
