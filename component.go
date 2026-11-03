package redstone

// maxPower is the strongest signal.
const maxPower = 15

// A torch flips until its count reaches burnOutFlips, then stops. The count stops
// at burnOutLimit, where the torch stays burnt out until a neighbour changes.
const (
	burnOutFlips = 16
	burnOutLimit = 33
)

// Phases a component evaluates in. They run cache, producer, other, lock.
const (
	phaseCache uint8 = 1 << iota
	phaseLock
	phaseProducer
	phaseOther
)

// source is one thing feeding a component.
type source struct {
	c *Component
	// damping is how much strength is lost on the way.
	damping int32
	pos     Pos
	face    Face
	// direct is a source that powers strongly along its path.
	direct bool
	// data is a rule's own mark: from the back for a repeater, reached through
	// another consumer for a consumer.
	data bool
}

// dest is a component fed by another, counted per source entry.
type dest struct {
	c *Component
	n int32
}

// Component is one part of a circuit, at one position.
type Component struct {
	kind               Kind
	pos                Pos
	chunk              Pos
	dir                Face
	strength, previous int16

	// sources feed the component; side are those that came in from the side, for
	// repeaters and comparators.
	sources, side []source
	dests         []dest

	ignoreFirst, first, needsUpdate bool
	allowUp, allowDown, removed     bool
	anyDirection                    bool

	// Producer, and the capacitors built on it.
	next      int32
	attach    bool
	stopPower bool
	allowed   uint8

	// Consumer. directlyPowered is whether the last evaluation's winner was
	// direct, sideInput a consumer that checks where wire comes from, viaDirect
	// that check's answer, halfPulse a consumer that sees half pulses as they are,
	// fromCapacitor one capacitors may feed from their own direction.
	directlyPowered, sideInput, viaDirect, halfPulse, fromCapacitor bool

	// Powered block.
	promoted, conducts bool

	// Wire.
	cached int32

	// Pulse.
	pulseOn bool

	// Torch.
	chain                                       *Component
	burn                                        int32
	on, half, changed, nextOn, nextHalf, burned bool

	// Repeater. on is shared with the torch.
	inputs                     [5]int32
	delay, flips               int32
	input, locked, lastIn, alt bool

	// Comparator. rear, left and right are analog strengths, -1 for none.
	rear, left, right      int32
	subtract, dirty        bool
	rearSignal, sideSignal int32
}

// newComponent makes a component of kind k with what every kind starts with.
func newComponent(k Kind) *Component {
	c := &Component{kind: k, first: true, dir: NoFace, allowDown: true}
	switch k {
	case KindPowered:
		c.conducts = true
	case KindConsumer:
		c.fromCapacitor = true
	case KindRepeater:
		c.locked = true
		c.inputs = [5]int32{2, 2, 2, 2, 0}
	case KindComparator:
		c.rear, c.left, c.right = -1, -1, -1
	case KindTorch:
		c.allowUp = true
	}
	return c
}

// Kind returns what the component is.
func (c *Component) Kind() Kind { return c.kind }

// Pos returns where the component is.
func (c *Component) Pos() Pos { return c.pos }

// Direction returns the direction the component was made with.
func (c *Component) Direction() Face { return c.dir }

// SetAttach makes a producer power the block it is attached to.
func (c *Component) SetAttach(on bool) { c.attach = on }

// SetAllowUp lets wire run up past this component.
func (c *Component) SetAllowUp(on bool) { c.allowUp = on }

// SetAnyDirection lets wire connect to this component from any side.
func (c *Component) SetAnyDirection(on bool) { c.anyDirection = on }

// SetIgnoreFirst skips the first report, for blocks that keep their saved state.
func (c *Component) SetIgnoreFirst(on bool) { c.ignoreFirst = on }

// SetAllowedFaces limits a directional producer to these faces, one bit each.
func (c *Component) SetAllowedFaces(faces uint8) { c.allowed = faces }

// SetSideInput makes a consumer look at where wire reaches it from.
func (c *Component) SetSideInput(on bool) { c.sideInput = on }

// SetFromCapacitor sets whether capacitors feed a consumer from their own side.
func (c *Component) SetFromCapacitor(on bool) { c.fromCapacitor = on }

// SetHalfPulse makes a consumer see a torch's half pulse as its real strength.
func (c *Component) SetHalfPulse(on bool) { c.halfPulse = on }

// SetDelay sets a repeater's delay in redstone ticks minus one, 0 to 3.
func (c *Component) SetDelay(delay int32) { c.delay = max(0, min(delay, 3)) }

// SetTorchOn sets a torch's state and forgets its burn out and any loop.
func (c *Component) SetTorchOn(on bool) {
	c.on, c.half, c.changed = on, false, true
	c.burn, c.burned, c.chain = 0, false, nil
}

// ResetBurnOut lets a burnt out torch try again, as a neighbour changing does.
func (c *Component) ResetBurnOut() {
	if c.burned {
		c.burn, c.burned = 0, false
	}
}

// BurntOut reports whether the torch has flipped enough to burn out.
func (c *Component) BurntOut() bool { return c.burn >= burnOutFlips }

// SetSubtract sets a comparator's mode.
func (c *Component) SetSubtract(on bool) { c.subtract = on }

// SetRearSignal sets the strength a comparator reads from a block behind it, -1
// for none, and has it read its sources from now on.
func (c *Component) SetRearSignal(signal int32) {
	c.rear, c.dirty = max(signal, -1), true
}

// LoadSignal restores a comparator's output from what its block saved, so loading
// it changes nothing until its inputs say so.
func (c *Component) LoadSignal(signal int32) {
	c.previous, c.strength = int16(signal), int16(signal) //nolint:gosec // at most maxPower
	c.next, c.rear = signal, signal
}

// Strength returns the component's strength, which depends on its kind.
func (c *Component) Strength() int16 {
	switch c.kind {
	case KindPowered:
		var best int32
		for i := range c.sources {
			best = max(best, int32(c.sources[i].c.Strength())-c.sources[i].damping)
		}
		return int16(best) //nolint:gosec // at most maxPower
	case KindTorch, KindRepeater:
		if c.on {
			return maxPower
		}
		return 0
	}
	return c.strength
}

// SetStrength sets what a producer gives out, or a plain component's strength.
func (c *Component) SetStrength(strength int32) {
	switch {
	case c.kind == KindPulse:
		c.next, c.pulseOn = strength, strength != 0
	case c.kind == KindRepeater:
		c.setRepeater(strength != 0)
	case c.kind == KindComparator || c.kind == KindProducer || c.kind == KindTorch:
		c.next = strength
		if c.first {
			c.strength = int16(strength) //nolint:gosec // strengths are small
		}
	default:
		c.strength = int16(strength) //nolint:gosec // strengths are small
	}
}

// setRepeater fills a repeater's delay line with a steady on or off.
func (c *Component) setRepeater(on bool) {
	c.on = on
	v := int32(2)
	if on {
		v = 3
	}
	for i := range c.inputs {
		if int32(i) <= c.delay { //nolint:gosec // i is below 5
			c.inputs[i] = v
		} else {
			c.inputs[i] = 0
		}
	}
}

// consumesAnyDirection is whether wire may join this component from any side.
func (c *Component) consumesAnyDirection() bool {
	switch c.kind {
	case KindRepeater, KindComparator:
		return true
	case KindPulse:
		return false
	}
	return c.anyDirection
}

// powerFace is the face a capacitor gives out from, NoFace for any.
func (c *Component) powerFace() Face {
	switch c.kind {
	case KindPulse, KindRepeater, KindComparator:
		return c.dir.Opposite()
	case KindTorch:
		return Up
	}
	return NoFace
}

// phases returns when the component evaluates.
func (c *Component) phases() uint8 {
	var p uint8
	switch c.kind {
	case KindRepeater:
		p = phaseCache | phaseLock
	case KindWire, KindTorch, KindComparator:
		p = phaseCache
	}
	if g := c.kind.group(); g == KindProducer || g == KindCapacitor {
		return p | phaseProducer
	}
	return p | phaseOther
}

// cache reads the component's inputs, before anything changes this round.
func (c *Component) cache() {
	switch c.kind {
	case KindWire:
		var best int32
		for i := range c.sources {
			best = max(best, int32(c.sources[i].c.Strength())-c.sources[i].damping)
		}
		c.cached = best
	case KindTorch:
		c.cacheTorch()
	case KindRepeater:
		c.cacheRepeater()
	case KindComparator:
		c.cacheComparator()
	}
}

// evaluate works out the new strength and reports whether the block must hear.
func (c *Component) evaluate() bool {
	switch c.kind {
	case KindConsumer:
		return c.evaluateConsumer()
	case KindProducer:
		c.strength = int16(c.next) //nolint:gosec // strengths are small
	case KindWire:
		changed := int16(c.cached) != c.strength //nolint:gosec // at most maxPower
		c.strength = int16(c.cached)             //nolint:gosec // at most maxPower
		return changed
	case KindPulse:
		c.strength = int16(c.next) //nolint:gosec // strengths are small
		on := c.pulseOn
		c.pulseOn, c.next = false, 0
		return on
	case KindTorch:
		c.on, c.half = c.nextOn, c.nextHalf
		if c.chain != nil && !c.burned && c.burn < burnOutLimit {
			c.burn++
		}
		return c.changed
	case KindRepeater:
		return c.evaluateRepeater()
	case KindComparator:
		return c.evaluateComparator()
	}
	return false
}

// evaluateConsumer takes the strongest source. A half pulse counts as full
// unless the consumer takes half pulses as they are.
func (c *Component) evaluateConsumer() bool {
	c.directlyPowered = false
	var best int32
	for i := range c.sources {
		s := &c.sources[i]
		v := int32(s.c.Strength()) - s.damping
		if s.c.kind == KindTorch && s.c.half && !c.halfPulse {
			v = maxPower - s.damping
		}
		if v > best {
			best = v
			c.directlyPowered = s.data
		}
	}
	changed := int16(best) != c.strength //nolint:gosec // at most maxPower
	c.strength = int16(best)             //nolint:gosec // at most maxPower
	return c.first || changed
}

// checkLock locks a repeater with a side input on, stopping its delay line.
func (c *Component) checkLock() {
	if c.kind != KindRepeater {
		return
	}
	c.locked = false
	for i := range c.side {
		if int32(c.side[i].c.Strength()) > c.side[i].damping {
			c.locked = true
			c.inputs[1], c.inputs[2], c.inputs[3] = c.inputs[0], c.inputs[0], c.inputs[0]
			return
		}
	}
}

// cacheRepeater reads the input and counts how long it has been flipping, so
// pulses shorter than the delay get stretched.
func (c *Component) cacheRepeater() {
	c.input = false
	for i := range c.sources {
		if int32(c.sources[i].c.Strength()) > c.sources[i].damping {
			c.input = true
			break
		}
	}
	if c.locked {
		return
	}
	if c.lastIn == c.input {
		c.flips = 0
		return
	}
	first := c.flips == 0
	c.flips++
	c.lastIn = !c.lastIn
	if first {
		c.alt = c.lastIn
	}
}

// evaluateRepeater moves the delay line on one step. Its slots hold 0 and 1 for
// a fresh off or on input, and 2 and 3 for an off or on already decided.
func (c *Component) evaluateRepeater() bool {
	if c.locked {
		return false
	}
	was := c.on
	d := c.delay
	in := int32(0)
	if c.input {
		in = 1
	}
	if d > 0 {
		copy(c.inputs[:d], c.inputs[1:d+1])
	}
	for i := d; i < 4; i++ {
		c.inputs[i] = in
	}
	if d > 0 {
		c.stepDelayLine(d)
	}
	c.on = c.inputs[0]&^2 == 1
	return was != c.on
}

// stepDelayLine settles the head of a repeater's delay line of delay d.
func (c *Component) stepDelayLine(d int32) {
	head := c.inputs[0]
	if c.flips > d && (!c.lastIn || c.flips != 2) {
		if head <= 1 {
			v := int32(2)
			if c.alt {
				v = 3
			}
			c.alt = !c.alt
			c.fillDelayLine(d, v)
		}
		return
	}
	switch {
	case head == 2 && c.inputs[1] == 0:
		c.inputs[1] = 2
	case head == 3 && c.inputs[1] == 1:
		c.inputs[1] = 3
	case head <= 1:
		var same int32
		for i := int32(0); i <= d; i++ {
			if c.inputs[i] == head {
				same++
			}
		}
		v := int32(3)
		if head == 0 && same > d {
			v = 2
		}
		c.fillDelayLine(d, v)
	}
}

// fillDelayLine sets the first d+1 slots of a delay line.
func (c *Component) fillDelayLine(d, v int32) {
	for i := int32(0); i <= d; i++ {
		c.inputs[i] = v
	}
}

// cacheComparator reads the back and side inputs. Only sources on a direct path
// count at the back.
func (c *Component) cacheComparator() {
	if !c.dirty {
		return
	}
	c.rearSignal, c.sideSignal = 0, 0
	for i := range c.sources {
		if c.sources[i].direct {
			c.rearSignal = max(c.rearSignal, int32(c.sources[i].c.Strength())-c.sources[i].damping)
		}
	}
	for i := range c.side {
		c.sideSignal = max(c.sideSignal, int32(c.side[i].c.Strength())-c.side[i].damping)
	}
}

// evaluateComparator compares or subtracts its inputs. Analog strengths set by
// the engine win over what its sources give.
func (c *Component) evaluateComparator() bool {
	old := c.strength
	c.previous = old
	rear := c.rear
	if rear == -1 {
		rear = c.rearSignal
	}
	side := c.sideSignal
	if c.left != -1 || c.right != -1 {
		side = max(c.left, c.right)
	}
	var out int32
	switch {
	case c.subtract:
		out = max(rear-side, 0)
	case side <= rear:
		out = rear
	}
	c.strength = int16(min(out, maxPower)) //nolint:gosec // at most maxPower
	return old != c.strength
}

// cacheTorch decides what a torch does next: off when powered, or flip while its
// own output comes back to it, until it burns out.
func (c *Component) cacheTorch() {
	if c.chain != nil {
		c.cacheChainedTorch()
		return
	}
	best, looped := c.strongestSource()
	var next, half, stop bool
	if looped {
		if !c.burned && c.burn < burnOutLimit {
			c.burn++
		}
		if c.burn < burnOutFlips {
			next, half = !c.on, true
		} else {
			half, stop = c.burn == burnOutFlips, c.burn >= burnOutLimit
		}
	} else {
		c.burn, c.burned = 0, false
		next = best == 0
	}
	c.nextHalf, c.nextOn, c.changed = half, next, c.on != next
	if stop {
		c.burned = true
	}
}

// cacheChainedTorch is a torch in a ring of torches, which pass the on state
// round in turn unless something else powers one of the blocks they sit on.
func (c *Component) cacheChainedTorch() {
	for i := range c.sources {
		s := c.sources[i].c
		if g := s.kind.group(); g != KindPowered && g != KindConsumer {
			continue
		}
		for j := range s.sources {
			in := &s.sources[j]
			if in.direct && in.c.kind != KindTorch && int32(in.c.Strength()) > in.damping {
				c.nextOn, c.nextHalf, c.changed = true, false, !c.on
				c.burn = -1
				return
			}
		}
	}
	c.nextHalf = false
	if c.burn != 0 {
		c.nextOn, c.changed = false, c.on
		return
	}
	c.nextOn, c.changed = true, !c.on
	n := c.chain
	n.burn = -1
	n = n.chain
	for range 4 {
		if n == nil || n == c {
			return
		}
		n.burn = 1
		n = n.chain
	}
}

// strongestSource finds the strongest thing powering a torch, and whether that
// is the torch's own output coming back to it.
func (c *Component) strongestSource() (best int32, looped bool) {
	var picked, self, other *source
	for i := range c.sources {
		s := &c.sources[i]
		g := s.c.kind.group()
		if g == KindProducer || (g == KindCapacitor && len(s.c.sources) == 0) {
			if v := int32(s.c.Strength()); v != 0 && v >= best {
				picked, best = s, v
			}
			continue
		}
		var last, selfValue int32
		for j := range s.c.sources {
			in := &s.c.sources[j]
			if !in.direct {
				continue
			}
			v := int32(in.c.Strength()) - in.damping
			last = max(v, 0)
			mine := in.pos == c.pos
			if v > 0 && last >= best {
				picked, best = in, v
				if !mine {
					other = in
				}
			}
			if mine {
				selfValue, self = last, in
			}
		}
		if other != nil && last == selfValue {
			self, picked = nil, other
		}
		if best == 0 && self != nil {
			picked = self
		}
		looped = self != nil && picked == self
	}
	return best, looped
}

// updateDependencies runs on every component after the relationships change.
func (c *Component) updateDependencies(g *System) {
	switch c.kind {
	case KindTorch:
		if !c.findRing(g) {
			c.chain = nil
		}
	case KindRepeater:
		c.moveToSide(func(s *source) bool { return !s.data })
	case KindComparator:
		c.sources = c.dropSources(c.sources, func(s *source) bool { return s.c.stopPower })
		back := c.dir.Opposite()
		c.moveToSide(func(s *source) bool { return s.face != back })
	}
}

// moveToSide moves the sources that are side inputs to the side list.
func (c *Component) moveToSide(isSide func(*source) bool) {
	kept := c.sources[:0]
	for i := range c.sources {
		s := c.sources[i]
		if isSide(&s) {
			s.direct, s.data, s.face = false, false, NoFace
			c.side = append(c.side, s)
			continue
		}
		kept = append(kept, s)
	}
	clear(c.sources[len(kept):])
	c.sources = kept
}

// ringOrder is the order torches around a block are linked into a ring.
var ringOrder = [...]Face{North, West, South, East}

// findRing looks for torches on the sides of a block with wire on top, feeding
// that block, which then take turns. It links them and reports whether it did.
func (c *Component) findRing(g *System) bool {
	found := false
	var seen []*Component
	for i := range c.sources {
		s := &c.sources[i]
		if w := g.live(s.pos.Side(Up)); w == nil || w.kind.group() != KindWire {
			continue
		}
		if k := s.c.kind.group(); k != KindConsumer && k != KindPowered {
			continue
		}
		for j := range s.c.sources {
			in := &s.c.sources[j]
			if !in.direct || in.c.kind != KindTorch || in.pos.Side(in.c.dir) != s.pos || in.c.dir == Down {
				continue
			}
			above := g.live(in.pos.Side(Up))
			if above == nil {
				continue
			}
			if k := above.kind.group(); k == KindPowered || k == KindConsumer {
				seen = append(seen, in.c)
			}
		}
		if len(seen) <= 1 {
			continue
		}
		var ring []*Component
		for _, f := range ringOrder {
			at := s.pos.Side(f)
			for _, t := range seen {
				if t.pos == at {
					ring = append(ring, t)
					break
				}
			}
		}
		if len(ring) > 0 {
			first, last := ring[0], ring[len(ring)-1]
			first.burn, last.burn = 0, 0
			last.chain = first
			for k := 0; k+1 < len(ring); k++ {
				ring[k].chain = ring[k+1]
				last.burn = burnOutFlips
			}
		}
		found = true
	}
	return found
}

// addSource is whether the component takes the signal t describes, and if so it
// is recorded. damping and direct are the path's and may change for what lies
// further on. It reports whether the signal goes on through the component.
func (c *Component) addSource(t *tracking, damping *int32, direct *bool) bool {
	switch c.kind {
	case KindConsumer:
		return c.addToConsumer(t, damping, direct)
	case KindPowered:
		return c.addToPowered(t, damping, direct)
	case KindWire:
		return c.addToWire(t, damping, direct)
	case KindTorch:
		g := t.nearest.kind
		if g != KindConsumer && g != KindPowered && g != KindWire && t.current.face == c.dir.Opposite() {
			c.track(t, *damping, *direct, false, false)
		}
	case KindRepeater:
		c.addToRepeater(t, damping, direct)
	case KindComparator:
		c.addToComparator(t, damping, direct)
	}
	return false
}

func (c *Component) addToConsumer(t *tracking, damping *int32, direct *bool) bool {
	kind := t.nearest.kind
	was := *direct
	*direct = false
	c.viaDirect = false
	if c.sideInput {
		switch kind {
		case KindPowered:
			if !was {
				return false
			}
		case KindProducer:
			on := t.nearest.c.attach && t.nearest.c.dir == t.current.face
			*direct, c.viaDirect = on, on
		case KindWire:
			*direct = true
		case KindCapacitor:
			*direct = t.current.face == Up
			if d := t.nearest.c.dir; d != NoFace {
				if pf := t.nearest.c.powerFace(); pf != NoFace {
					d = pf
				}
				on := t.current.face == d
				*direct, c.viaDirect = on, on
			}
		}
	} else if !was && kind == KindPowered {
		return false
	}
	if kind == KindCapacitor && !c.fromCapacitor && t.nearest.c.dir == t.current.face {
		return false
	}
	added := c.track(t, *damping, *direct, kind == KindConsumer, false)
	return added && *direct
}

func (c *Component) addToPowered(t *tracking, damping *int32, direct *bool) bool {
	if !c.conducts || c.allowUp {
		return false
	}
	was := *direct
	c.promoted = false
	switch t.nearest.kind {
	case KindConsumer, KindPowered:
		*direct = false
		return false
	case KindProducer:
		on := t.nearest.c.attach && t.current.face == t.nearest.c.dir
		*direct, c.promoted = on, on
	case KindWire:
		f := t.current.face
		*direct = was || (t.nearest.face == f && t.nearest.pos.Y == t.current.pos.Y) || f == Down
	case KindCapacitor:
		on := t.current.face == t.nearest.c.powerFace()
		*direct, c.promoted = on, on
		if !on && t.nearest.c.kind == KindTorch {
			return false
		}
	}
	return c.track(t, *damping, *direct, false, false)
}

func (c *Component) addToWire(t *tracking, damping *int32, direct *bool) bool {
	kind := t.nearest.kind
	if kind == KindPowered && !*direct {
		return false
	}
	on := true
	f := t.current.face
	if kind != KindProducer && kind != KindCapacitor && kind != KindWire && f != Down &&
		f != t.nearest.face && f != t.nearest.face.Opposite() {
		switch kind {
		case KindConsumer:
			on = t.nearest.c.viaDirect
		case KindPowered:
			on = t.nearest.c.promoted
		default:
			on = false
		}
	}
	*direct = on
	*damping++
	return c.track(t, *damping, on, false, false)
}

func (c *Component) addToRepeater(t *tracking, damping *int32, direct *bool) {
	back := t.current.face == c.dir.Opposite()
	if t.current.face == c.dir {
		return
	}
	if t.nearest.kind == KindPowered && (!t.nearest.c.hasDirectSource() || !*direct) {
		return
	}
	if k := t.nearest.c.kind; back || k == KindRepeater || k == KindComparator {
		c.track(t, *damping, *direct, back, false)
	}
}

func (c *Component) addToComparator(t *tracking, damping *int32, direct *bool) {
	f := t.current.face
	if f < North || f == c.dir {
		return
	}
	k := t.nearest.c.kind
	if f != c.dir.Opposite() && k != KindWire && k != KindRepeater && k != KindComparator {
		return
	}
	if t.nearest.kind == KindPowered && !t.nearest.c.hasDirectSource() {
		return
	}
	c.track(t, *damping, *direct, false, true)
}

// hasDirectSource reports whether something feeds the component directly.
func (c *Component) hasDirectSource() bool {
	for i := range c.sources {
		if c.sources[i].direct {
			return true
		}
	}
	return false
}

// track records the signal's origin as a source, keeping the shortest path to it,
// and reports whether anything changed. Comparators keep one per face.
func (c *Component) track(t *tracking, damping int32, direct, data, perFace bool) bool {
	d := max(damping-1, 0)
	for i := range c.sources {
		s := &c.sources[i]
		if s.pos != t.origin.pos || (perFace && s.face != t.current.face) {
			continue
		}
		if direct && !s.direct {
			s.direct = true
			if t.origin.c != nil && int32(t.origin.c.Strength()) < max(damping, 1) {
				d = max(min(s.damping, d), 0)
			}
			s.damping = d
			return true
		}
		if s.damping > d {
			s.damping = d
			return true
		}
		return false
	}
	c.addSourceItem(source{c: t.origin.c, damping: d, pos: t.origin.pos, face: t.current.face, direct: direct, data: data})
	return true
}

// addSourceItem adds a source, or replaces the entry for the same one and face.
func (c *Component) addSourceItem(s source) {
	for i := range c.sources {
		if c.sources[i].c == s.c && c.sources[i].face == s.face {
			c.sources[i] = s
			return
		}
	}
	c.sources = append(c.sources, s)
	s.c.addDest(c)
}

// addDest counts to as fed by this component once more.
func (c *Component) addDest(to *Component) {
	for i := range c.dests {
		if c.dests[i].c == to {
			c.dests[i].n++
			return
		}
	}
	c.dests = append(c.dests, dest{c: to, n: 1})
}

// dropDest counts to as fed by this component once less.
func (c *Component) dropDest(to *Component) {
	for i := range c.dests {
		if c.dests[i].c != to {
			continue
		}
		if c.dests[i].n--; c.dests[i].n <= 0 {
			c.dests = append(c.dests[:i], c.dests[i+1:]...)
		}
		return
	}
}

// removeSourceAt drops every source at pos, in both lists.
func (c *Component) removeSourceAt(pos Pos, gone *Component) {
	at := func(s *source) bool { return s.pos == pos }
	c.sources = c.dropSources(c.sources, at)
	c.side = c.dropSources(c.side, at)
	if c.kind == KindTorch && gone != nil && c.chain == gone {
		c.chain = gone.chain
	}
}

// removeSourceOf drops every source that is from.
func (c *Component) removeSourceOf(from *Component) {
	is := func(s *source) bool { return s.c == from }
	c.sources = c.dropSources(c.sources, is)
	c.side = c.dropSources(c.side, is)
}

// dropSources removes the entries drop says, keeping the order.
func (c *Component) dropSources(list []source, drop func(*source) bool) []source {
	kept := list[:0]
	for i := range list {
		if drop(&list[i]) {
			list[i].c.dropDest(c)
			continue
		}
		kept = append(kept, list[i])
	}
	clear(list[len(kept):])
	return kept
}

// hasSource reports whether from feeds the component.
func (c *Component) hasSource(from *Component) bool {
	for i := range c.sources {
		if c.sources[i].c == from {
			return true
		}
	}
	return false
}

// allowConnection is whether a signal may go on from this component, the path's
// nearest, to the one t describes. It may set direct for what comes after.
func (c *Component) allowConnection(g *System, t *tracking, direct *bool) bool {
	switch c.kind {
	case KindConsumer:
		switch t.current.kind {
		case KindWire:
			if c.sideInput {
				return c.viaDirect
			}
		case KindCapacitor:
			pf := t.current.c.powerFace()
			if pf == NoFace {
				return t.current.face == Up && t.direct
			}
			return t.current.face == pf && t.direct
		}
		return t.direct
	case KindPowered:
		if t.current.kind == KindWire {
			return c.promoted
		}
		return t.current.kind != KindPowered
	case KindProducer:
		if t.current.face != NoFace && c.allowed != 0 && c.allowed&(1<<t.current.face) == 0 {
			return false
		}
		return c.attach || t.current.kind != KindPowered
	case KindPulse, KindRepeater, KindComparator:
		return t.current.face == c.dir.Opposite()
	case KindTorch:
		return c.allowTorch(t)
	case KindWire:
		return c.allowWire(g, t, direct)
	}
	return true
}

// allowTorch also records what the torch stands on as feeding the torch, so it
// hears its own output come back.
func (c *Component) allowTorch(t *tracking) bool {
	f, k := t.current.face, t.current.kind
	if ((k == KindProducer || k == KindPowered) && f == t.nearest.face) || (k == KindConsumer && c.dir == f) {
		from := *t
		from.origin = t.current
		c.track(&from, t.damping, t.direct, false, false)
		if k == KindPowered {
			return false
		}
	}
	return c.dir != Down || f != Down
}

// allowWire is where wire may run on to.
func (c *Component) allowWire(g *System, t *tracking, direct *bool) bool {
	f := t.current.face
	if f == Up {
		return false
	}
	if f == Down || t.current.c.consumesAnyDirection() {
		return true
	}
	if t.current.kind == KindWire {
		return g.wireStep(t)
	}
	cur, near, far := t.current.pos, t.nearest.pos, t.second.pos
	dx, dy, dz := far.X-cur.X, far.Y-cur.Y, far.Z-cur.Z
	if dz == 2 || dz == -2 {
		return !g.flowsTo(near, East) && !g.flowsTo(near, West)
	}
	if dx == 2 || dx == -2 {
		return !g.flowsTo(near, North) && !g.flowsTo(near, South)
	}
	diagonal := (dy == 1 || dy == -1) && (dx != 0 || dz != 0)
	beside := dy == 0 && (t.current.kind == KindConsumer || t.current.kind == KindPowered) && dx != 0 && dz != 0
	if diagonal || beside {
		on := g.dotOrLine(near, near.X-cur.X, near.Z-cur.Z)
		*direct = on
		return on
	}
	switch t.second.kind {
	case KindConsumer:
		if !t.second.c.viaDirect {
			return false
		}
	case KindProducer:
	case KindPowered:
		if !t.second.c.promoted {
			return false
		}
	default:
		if t.second.c.kind != KindTorch {
			return false
		}
	}
	if g.flowsTo(near, North) || g.flowsTo(near, South) || g.flowsTo(near, East) || g.flowsTo(near, West) {
		return false
	}
	*direct = true
	return true
}
