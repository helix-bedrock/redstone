# Status

## Status as of 2026-11-03

First pass, from BDS 1.26.60. Not yet tested in game or compared with BDS.

**Built:** wire, torches with burn out and torch rings, repeaters with delay and locking, comparators,
producers, directional producers, pulse capacitors, consumers, powered blocks, path finding, and
evaluation every 2 game ticks in BDS's four phases.

**Not built:** piston consumers and rail transporters.

**Read in the older 1.21.3 build only, to be checked against 1.26.60:** which neighbours are queued
again when a component is removed. Queuing more than BDS only works paths out again, so the result
should be the same.
