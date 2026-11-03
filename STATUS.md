# Status

## Status as of 2026-11-03

First pass, from BDS 1.26.60. Not yet tested in game or compared with BDS.

**Built:** wire, torches with burn out and torch rings, repeaters with delay and locking, comparators,
producers, directional producers, pulse capacitors, consumers, powered blocks, path finding, and
evaluation every 2 game ticks in BDS's four phases.

**Not built:** piston consumers and rail transporters.

**Also here:** comparators restored from their saved output (`LoadSignal`), `SignalTick` for block
actors that read on BDS's ticks, and `Merge` and `SplitOff` to move circuits between Systems without
rebuilding them. Removal queues what BDS 1.26.60 queues.
