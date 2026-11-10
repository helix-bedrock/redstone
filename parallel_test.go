package redstone

import (
	"sync"
	"testing"
)

// buildCircuits lays out n separate lever, wire, repeater and lamp lines, one per z, and a
// torch on a lever's block in each, so islands of several kinds exist side by side.
func buildCircuits(g *System, w *testWorld, n int32) []*Component {
	var levers []*Component
	for z := range n {
		z *= 4
		l := lever(g, Pos{0, 0, z}, z%8 == 0)
		levers = append(levers, l)
		g.Create(Pos{1, 0, z}, KindWire, Down)
		g.Create(Pos{2, 0, z}, KindWire, Down)
		g.Create(Pos{3, 0, z}, KindRepeater, West)
		lamp := g.Create(Pos{4, 0, z}, KindConsumer, NoFace)
		lamp.SetSideInput(true)
		lamp.SetFromCapacitor(false)
		w.power[Pos{1, 0, z + 1}] = PowerConductor
		tl := lever(g, Pos{0, 0, z + 1}, false)
		tl.dir = East
		levers = append(levers, tl)
		torch := g.Create(Pos{2, 0, z + 1}, KindTorch, West)
		torch.SetTorchOn(true)
	}
	return levers
}

// backwards runs the islands last to first, half of them on another goroutine: the order
// must not matter.
func backwards(n int, fn func(i int)) {
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := n - 1; i >= n/2; i-- {
			fn(i)
		}
	})
	for i := n/2 - 1; i >= 0; i-- {
		fn(i)
	}
	wg.Wait()
}

// TestEvaluateParallelMatches checks that evaluating islands in any order on several
// goroutines gives what Evaluate gives, round by round.
func TestEvaluateParallelMatches(t *testing.T) {
	const lines = 12
	a, wa := New(), newTestWorld()
	b, wb := New(), newTestWorld()
	la, lb := buildCircuits(a, wa, lines), buildCircuits(b, wb, lines)
	for round := range 30 {
		if round%5 == 0 {
			for i := range la {
				if (i+round)%3 == 0 {
					s := maxPower - int32(la[i].Strength())
					la[i].SetStrength(s)
					lb[i].SetStrength(s)
				}
			}
		}
		a.UpdateDependencies(wa)
		a.Evaluate(wa)
		a.UpdateChunk(Pos{}, wa)
		b.UpdateDependencies(wb)
		b.EvaluateParallel(wb, backwards)
		b.UpdateChunk(Pos{}, wb)
		if len(b.islands) < lines {
			t.Fatalf("round %d: %d islands, want at least %d", round, len(b.islands), lines)
		}
		for p, c := range a.all {
			if got, want := b.Strength(p), c.Strength(); got != want {
				t.Fatalf("round %d: %v strength %d, sequential %d", round, p, got, want)
			}
		}
		for p, s := range wa.changes {
			if wb.changes[p] != s {
				t.Fatalf("round %d: %v reported %d, sequential %d", round, p, wb.changes[p], s)
			}
		}
	}
}
