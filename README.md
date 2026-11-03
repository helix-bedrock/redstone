# redstone

Vanilla Minecraft: Bedrock Edition redstone circuits for any Go server: which
components feed which, how strong each signal is and in what order they update.

It is part of [Helix](https://github.com/helix-bedrock), but it is its own library. It knows no
block IDs or names. An engine creates a component for each redstone block, answers the questions in
`World`, and turns `PowerChanged` into block changes.

Status: see [STATUS.md](STATUS.md).

## How it is checked

Behaviour is read from Bedrock Dedicated Server 1.26.60. The decompiled code is **not** in this repo.
It was only used to learn how the game works.

## Quick start

```go
sys := redstone.New()

// When a block is placed:
wire := sys.Create(redstone.Pos{X: 1}, redstone.KindWire, redstone.Down)
_ = wire

// Every game tick, for w implementing redstone.World:
sys.UpdateDependencies(w)
if sys.Tick() {
	sys.Evaluate(w)
}
sys.UpdateChunk(chunkOrigin, w) // for each chunk, after its block ticks
```

A `System` is used from one goroutine. Give each unit that ticks on its own its own `System`.
