# Screenshots

Pairs of the same page, same data, same viewport, taken from two builds:
`before-*` from `main`, `after-*` from this branch.

How they were produced, so they can be redone:

1. A copy of a real SQLite database, truncated to end during a busy period
   and shifted so its newest sample is "now". The GPU numbers, the charts and
   the host panel are collected data, not fixtures.
2. Both builds served that copy in hub mode, on their own port and their own
   copy of the database.
3. `google-chrome --headless=new --force-dark-mode --window-size=... --screenshot=...`
   against each page in turn.

Two things in the `after-*` shots are seeded rather than collected, because
the machine they came from had neither during the capture:

- the alert journal entries (one open, three cleared), seeded so the page has
  something to show;
- the throttle mask on the last few minutes of samples, and the device
  capability flags, set to what the binary reports on that same card
  (throttle reasons yes, ECC no).

The GPU UUID is masked.
