package redstone

import "testing"

// testWorld is a world where everything is loaded and some blocks conduct.
type testWorld struct {
	power   map[Pos]Power
	changes map[Pos]int16
}

func newTestWorld() *testWorld {
	return &testWorld{power: map[Pos]Power{}, changes: map[Pos]int16{}}
}

func (*testWorld) Loaded(Pos) bool                          { return true }
func (*testWorld) ChunksLoaded(Pos, int32) bool             { return true }
func (w *testWorld) BlockPower(p Pos) Power                 { return w.power[p] }
func (w *testWorld) PowerChanged(p Pos, s, _ int16, _ bool) { w.changes[p] = s }

// run ticks the system n redstone rounds.
func run(g *System, w *testWorld, n int) {
	for range n {
		g.UpdateDependencies(w)
		g.Evaluate(w)
		g.UpdateChunk(Pos{}, w)
	}
}

// lever makes a producer that is on or off.
func lever(g *System, p Pos, on bool) *Component {
	c := g.Create(p, KindProducer, Up)
	c.SetAttach(true)
	if on {
		c.SetStrength(maxPower)
	}
	return c
}

func TestLeverWireLamp(t *testing.T) {
	g, w := New(), newTestWorld()
	l := lever(g, Pos{0, 0, 0}, true)
	for x := int32(1); x <= 3; x++ {
		g.Create(Pos{x, 0, 0}, KindWire, Down)
	}
	lamp := g.Create(Pos{4, 0, 0}, KindConsumer, NoFace)
	lamp.SetSideInput(true)
	lamp.SetFromCapacitor(false)
	run(g, w, 3)
	if got := g.Strength(Pos{1, 0, 0}); got != 15 {
		t.Fatalf("first wire: strength %d, want 15", got)
	}
	if got := g.Strength(Pos{3, 0, 0}); got != 13 {
		t.Fatalf("last wire: strength %d, want 13", got)
	}
	if got := w.changes[Pos{4, 0, 0}]; got != 13 {
		t.Fatalf("lamp heard %d, want 13", got)
	}
	l.SetStrength(0)
	run(g, w, 2)
	if got := w.changes[Pos{4, 0, 0}]; got != 0 {
		t.Fatalf("lamp heard %d after the lever went off, want 0", got)
	}
}

func TestWireRunsOut(t *testing.T) {
	g, w := New(), newTestWorld()
	lever(g, Pos{0, 0, 0}, true)
	for x := int32(1); x <= 17; x++ {
		g.Create(Pos{x, 0, 0}, KindWire, Down)
	}
	run(g, w, 3)
	if got := g.Strength(Pos{15, 0, 0}); got != 1 {
		t.Fatalf("wire 15: strength %d, want 1", got)
	}
	if got := g.Strength(Pos{16, 0, 0}); got != 0 {
		t.Fatalf("wire 16: strength %d, want 0", got)
	}
}

func TestRepeaterDelay(t *testing.T) {
	g, w := New(), newTestWorld()
	l := lever(g, Pos{0, 0, 0}, false)
	// A repeater's direction is the side its input comes from.
	r := g.Create(Pos{1, 0, 0}, KindRepeater, West)
	r.SetAttach(true)
	r.SetDelay(1)
	g.Create(Pos{2, 0, 0}, KindWire, Down)
	run(g, w, 3)
	if r.Strength() != 0 {
		t.Fatalf("repeater on with its input off")
	}
	l.SetStrength(maxPower)
	rounds := 0
	for r.Strength() == 0 && rounds < 10 {
		run(g, w, 1)
		rounds++
	}
	if rounds < 2 || rounds >= 10 {
		t.Fatalf("repeater of delay 1 turned on after %d rounds", rounds)
	}
}

func TestTorchInverts(t *testing.T) {
	g, w := New(), newTestWorld()
	w.power[Pos{1, 0, 0}] = PowerConductor
	l := lever(g, Pos{0, 0, 0}, false)
	l.dir = East
	// A torch's direction is the side its block is on.
	torch := g.Create(Pos{2, 0, 0}, KindTorch, West)
	torch.SetTorchOn(true)
	run(g, w, 3)
	if torch.Strength() != maxPower {
		t.Fatalf("torch off with nothing powering it")
	}
	l.SetStrength(maxPower)
	run(g, w, 3)
	if torch.Strength() != 0 {
		t.Fatalf("torch still on with its block powered")
	}
	l.SetStrength(0)
	run(g, w, 3)
	if torch.Strength() != maxPower {
		t.Fatalf("torch did not come back on")
	}
}

func TestRemoveDropsSource(t *testing.T) {
	g, w := New(), newTestWorld()
	lever(g, Pos{0, 0, 0}, true)
	g.Create(Pos{1, 0, 0}, KindWire, Down)
	run(g, w, 2)
	if got := g.Strength(Pos{1, 0, 0}); got != 15 {
		t.Fatalf("wire: strength %d, want 15", got)
	}
	g.Remove(Pos{0, 0, 0})
	run(g, w, 2)
	if got := g.Strength(Pos{1, 0, 0}); got != 0 {
		t.Fatalf("wire: strength %d after its lever went, want 0", got)
	}
}

func TestInterval(t *testing.T) {
	g := New()
	var due []bool
	for range 5 {
		due = append(due, g.Tick())
	}
	want := []bool{true, false, true, false, true}
	for i := range want {
		if due[i] != want[i] {
			t.Fatalf("tick %d: due %v, want %v", i, due[i], want[i])
		}
	}
}
