// Package redstone is a one-to-one copy of how Bedrock Dedicated Server 1.26.60
// runs its circuits: which components feed which, how strong each signal is and
// in what order they update. It knows nothing about blocks or any server. An
// engine creates components for its blocks, answers the few questions in World
// and turns PowerChanged into block changes.
//
// A System is not safe for use from more than one goroutine. Give each unit that
// ticks on its own, such as a dimension or a region, its own System.
package redstone

// Pos is a block position.
type Pos struct{ X, Y, Z int32 }

// Side returns the position next to p on face f.
func (p Pos) Side(f Face) Pos {
	switch f {
	case Down:
		p.Y--
	case Up:
		p.Y++
	case North:
		p.Z--
	case South:
		p.Z++
	case West:
		p.X--
	case East:
		p.X++
	}
	return p
}

// Chunk returns the origin of the chunk holding p, which keys the chunk lists.
func (p Pos) Chunk() Pos { return Pos{p.X &^ 15, 0, p.Z &^ 15} }

// Face is a side of a block, numbered as BDS numbers them.
type Face uint8

// The faces, then NoFace for something that has no direction.
const (
	Down Face = iota
	Up
	North
	South
	West
	East
	NoFace
)

// Opposite returns the face across the block from f. NoFace stays NoFace.
func (f Face) Opposite() Face {
	if f >= NoFace {
		return f
	}
	return f ^ 1
}

// Kind is what a component is. The high half is its group, which is what most of
// the circuit rules look at.
type Kind uint32

// The kinds, with BDS's values.
const (
	KindConsumer   Kind = 0x20000
	KindPowered    Kind = 0x40000
	KindProducer   Kind = 0x80000
	KindWire       Kind = 0x100000
	KindCapacitor  Kind = 0x200000
	KindComparator Kind = 0x200001
	KindPulse      Kind = 0x200002
	KindTorch      Kind = 0x200003
	KindRepeater   Kind = 0x200004
)

// group returns the group k belongs to.
func (k Kind) group() Kind { return k & 0xFFFF0000 }

// Power is what a block that is not a component does with power it is given.
type Power uint8

const (
	// PowerNone is a block that takes no part.
	PowerNone Power = iota
	// PowerConductor is a block that carries power on to what touches it.
	PowerConductor
	// PowerInsulator is a block that carries no power but lets wire climb it.
	PowerInsulator
)

// World is what a System asks of the engine. It is called from the System's
// methods only, on the goroutine that called them.
type World interface {
	// Loaded reports whether the block at p can be read now.
	Loaded(p Pos) bool
	// ChunksLoaded reports whether every chunk within radius blocks of p is
	// loaded. Components of chunks where it is false do not evaluate.
	ChunksLoaded(p Pos, radius int32) bool
	// BlockPower says what the block at p does with power.
	BlockPower(p Pos) Power
	// PowerChanged tells the engine the component at p has a new strength. first
	// is true the first time it reports.
	PowerChanged(p Pos, strength, previous int16, first bool)
}
