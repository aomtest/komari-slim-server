# Changelog

All notable changes to komari-slim are documented here.

Each release is tagged in the server repository, but most user-visible changes
land in the web repository, and many releases ship without any new server
commit at all. GitHub's auto-generated release notes therefore cannot describe
them — they only ever produce a compare link. So entries are written by hand.

The release workflow extracts the section matching the pushed tag and uses it as
the release body. A tag without a matching section fails the release on purpose.

## v0.1.20 - 2026-09-23

### Fixed

- Signing in no longer leaves `Error: HTTP 401:` on the home page for a few
  seconds before the server cards appear. With a private site enabled, the home
  page's node request is rejected while signed out, and that error was kept in
  app-level state that signing in did not clear; it lingered until the next
  5-second poll. The login flow now refreshes the node list once the session is
  established.

### Internal

- The release workflow now retries the Go module download up to five times and
  sets a `GOPROXY` fallback list. v0.1.20 initially published no assets because
  one of the eight build jobs hit a transient `proxy.golang.org` error, and a
  single failure in the matrix skips the whole release.

## v0.1.19 - 2026-09-23

### Changed

- Passwords are now hashed with bcrypt instead of `SHA256(password + salt)` with
  a salt hardcoded in the source. New passwords get a per-hash random salt.
  Existing hashes keep working and are upgraded transparently on the next
  successful sign-in — no password reset, no schema change.

  Note: rolling back to an older build after a hash has been upgraded locks that
  account out, because the old code compares a SHA256 digest against a bcrypt
  string. `komari chpasswd -p <password>` reads the database directly and works
  without signing in, so it is the recovery path. Back up `komari.db` first.

### Internal

- Added a build workflow that runs `go build ./...` and `go test ./...` on every
  push. The release workflow only builds the main package and runs no tests, so
  broken test files were previously invisible until release time.

## v0.1.18 - 2026-09-21

### Fixed

- The notification channels page works again. It had been showing
  `RPC Error -32601: method not found`. An upstream web commit rewrote the page
  to call three RPC methods that the server does not implement, and the server
  side of that refactor has not landed upstream either. The page now uses the
  existing `/api/admin/settings/message-sender` endpoints, which the server has
  always served.

## v0.1.17 - 2026-09-21

### Fixed

- The app icon no longer shows white corners on non-white backgrounds. The
  generator filled the area outside the circular mask with white and then dropped
  the alpha channel; the area is now transparent, matching the previous icon.

## v0.1.16 - 2026-09-21

### Fixed

- Long CPU model names are no longer cut off on the node cards. Trademark markers
  (`(R)`, `(TM)`, `(C)`, `®`, `™`) are stripped, which brings
  `Intel(R) Xeon(R) Platinum 8272CL CPU @ 2.60GHz` down to 40 characters and back
  onto one line; anything still too long wraps to two lines instead of being
  truncated. Model numbers, core counts and clock speeds are kept as-is.

## v0.1.15 - 2026-09-21

### Added

- Node cards show the OS version (`Debian 12`, `Ubuntu 22.04`) instead of just
  the distribution name, and the CPU model below the CPU bar.
- Memory is split into separate RAM and Swap rows, each with its own bar and
  used/total figure. Hosts without swap do not get an empty swap row.

### Changed

- The footer is centred again.

## v0.1.14 - 2026-09-21

### Changed

- The footer was left-aligned. (Reverted in v0.1.15.)

## v0.1.13 - 2026-09-21

### Changed

- The accent colour is a brand blue instead of the upstream iris.
- The "Documentation" entry was removed from the sidebar. It pointed at the
  upstream project's documentation site, which does not describe this fork.

---

Versions before v0.1.13 predate this changelog. They covered the fork itself:
rebranding to komari-slim, replacing the logo, trimming the execution surfaces
(terminal, file manager, command dispatch, plugins, JavaScript notification
channel) and their leftover menu entries, fixing the site-name injection that
had silently stopped working, and extending the release matrix to the eight
platforms the install script expects.
