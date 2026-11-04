# Status

## Status as of 2026-11-04

Written from BDS 1.26.60's code and not fully tested. Basic wire, repeaters, torches, lamps and comparators have been tried in game through Helix; nothing has been compared with BDS side by side.

**Built:** wire, torches with burn out and torch rings, repeaters with delay and locking, comparators,
producers, directional producers, pulse capacitors, consumers, powered blocks, path finding, and
evaluation every 2 game ticks in BDS's four phases.

**Rails:** `KindRail` is BDS's rail transporter: a reach of nine from a source, carried only along rails of the same group and direction. Its strength is that reach, not a signal strength.

A piston is a consumer with one blocked face (`SetBlockedFace`).

**Observers** are pulse capacitors, as in BDS; nothing new was needed for them here.

**Also here:** comparators restored from their saved output (`LoadSignal`), `SignalTick` for block
actors that read on BDS's ticks, and `Merge` and `SplitOff` to move circuits between Systems without
rebuilding them. Removal queues what BDS 1.26.60 queues.
