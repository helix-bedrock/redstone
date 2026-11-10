package redstone

import (
	"cmp"
	"slices"
)

// DefaultInterval is how many game ticks pass between evaluations, BDS's value.
const DefaultInterval = 2

// Rules are the numbers circuits run by. [DefaultRules] gives BDS's; anything else
// stops being vanilla, on purpose.
type Rules struct {
	// SignalReach is how far a signal may travel, in blocks of wire. A signal is
	// used up after 15 whatever this says, so more than 15 changes nothing there.
	SignalReach int32
	// BurnOutFlips is how many flips a torch makes before it stops.
	BurnOutFlips int32
	// BurnOutLimit is where that count stops, and the torch stays burnt out until a
	// neighbour changes.
	BurnOutLimit int32
	// RailReach is how far rails carry the power of one source, counting the first.
	RailReach int32
	// LoadRadius is how many blocks around a chunk must be loaded for its components
	// to evaluate.
	LoadRadius int32
}

// DefaultRules returns BDS's numbers.
func DefaultRules() Rules {
	return Rules{SignalReach: 15, BurnOutFlips: 16, BurnOutLimit: 33, RailReach: 9, LoadRadius: 32}
}

// sides are the four horizontal faces in BDS's order.
var sides = [...]Face{North, South, West, East}

// entry is a component in its chunk's list, with the phases it runs in.
type entry struct {
	c      *Component
	phases uint8
}

// chunkList is the components of one chunk, in the order they were added.
type chunkList struct {
	origin  Pos
	entries []entry
	loaded  bool
}

// related is something a signal from a position reached or tried to.
type related struct {
	c   *Component
	pos Pos
}

// pending is a position waiting in one of the System's queues.
type pending struct {
	pos Pos
	c   *Component
}

// slot is a component on a path: which it is, where, the face the path entered
// it by and its group.
type slot struct {
	c    *Component
	pos  Pos
	face Face
	kind Kind
}

// tracking is a signal on its way through the graph.
type tracking struct {
	// current is the component being tried, origin where the signal started,
	// nearest the one it comes from and second the one before that.
	current, origin, nearest, second slot
	damping                          int32
	direct                           bool
}

// System is the circuits of one dimension or region. Its methods must be called
// from one goroutine, in the order a tick does: UpdateDependencies, Evaluate on
// the ticks Tick says, then UpdateChunk for each chunk as it ticks.
type System struct {
	// Interval is how many game ticks pass between evaluations. It may be changed
	// at any time.
	Interval int

	// rules is shared with every component here, so changing it reaches them all.
	rules *Rules

	locked    bool
	counter   int
	evaluated bool

	all      map[Pos]*Component
	order    []*Component
	chunks   []*chunkList
	byChunk  map[Pos]*chunkList
	relation map[Pos][]related
	waiting  map[Pos][]Pos

	adds     []pending
	updates  []pending
	updating map[Pos]bool
	removals []pending

	queue []tracking

	// islands are the circuits that share nothing, for EvaluateParallel; stale is set
	// when the graph changed since they were found. islandFn evaluates one.
	islands  []island
	stale    bool
	islandFn func(i int)
}

// New returns an empty System.
func New() *System {
	rules := DefaultRules()
	return &System{
		Interval: DefaultInterval, counter: DefaultInterval, rules: &rules,
		all: map[Pos]*Component{}, byChunk: map[Pos]*chunkList{},
		relation: map[Pos][]related{}, waiting: map[Pos][]Pos{}, updating: map[Pos]bool{},
	}
}

// Rules returns the numbers this System runs by.
func (g *System) Rules() Rules { return *g.rules }

// SetRules changes the numbers this System runs by, from the next thing that reads
// one. Paths already found keep the reach they were found with until they are worked
// out again.
func (g *System) SetRules(r Rules) { *g.rules = r }

// Lock stops components being created or removed while locked. An engine holds
// it while it sets a block because of the circuit, so setting that block does
// not rebuild the circuit it came from.
func (g *System) Lock(on bool) { g.locked = on }

// Locked reports whether the System is locked.
func (g *System) Locked() bool { return g.locked }

// live returns the component at pos unless it is being removed.
func (g *System) live(pos Pos) *Component {
	if c, ok := g.all[pos]; ok && !c.removed {
		return c
	}
	return nil
}

// Component returns the component of kind k at pos, whether it is in the graph or
// waiting to join it, or nil.
func (g *System) Component(pos Pos, k Kind) *Component {
	if c := g.live(pos); c != nil && (c.kind == k || c.kind.group() == k) {
		return c
	}
	for i := range g.adds {
		if c := g.adds[i].c; g.adds[i].pos == pos && (c.kind == k || c.kind.group() == k) {
			return c
		}
	}
	return nil
}

// At returns whatever component is at pos, or nil.
func (g *System) At(pos Pos) *Component {
	if c := g.live(pos); c != nil {
		return c
	}
	for i := range g.adds {
		if g.adds[i].pos == pos {
			return g.adds[i].c
		}
	}
	return nil
}

// Strength returns the strength of the component at pos, or -1 without one.
func (g *System) Strength(pos Pos) int16 {
	if c := g.live(pos); c != nil {
		return c.Strength()
	}
	return -1
}

// Create makes a component of kind k at pos with direction dir, or returns the
// one already there of that kind. It joins the graph at the next
// UpdateDependencies. While locked it only looks.
func (g *System) Create(pos Pos, k Kind, dir Face) *Component {
	if c := g.Component(pos, k); c != nil || g.locked {
		return c
	}
	c := newComponent(k, g.rules)
	c.pos, c.chunk, c.dir = pos, pos.Chunk(), dir
	g.add(pos, c)
	return g.Component(pos, k)
}

// add queues c at pos. A plain powered block never replaces something in the
// queue, and a real component replaces a powered block unless that one stays.
func (g *System) add(pos Pos, c *Component) {
	for i := range g.adds {
		if g.adds[i].pos != pos {
			continue
		}
		old := g.adds[i].c
		switch {
		case old.kind == KindPowered && old.allowUp:
			return
		case old.kind != KindPowered && c.kind == KindPowered && !c.allowUp:
			return
		}
		g.adds[i].c = c
		return
	}
	g.adds = append(g.adds, pending{pos: pos, c: c})
}

// Remove takes the component at pos out at the next UpdateDependencies.
func (g *System) Remove(pos Pos) {
	if g.locked {
		return
	}
	for i := range g.adds {
		if g.adds[i].pos == pos {
			g.adds = append(g.adds[:i], g.adds[i+1:]...)
			break
		}
	}
	if c := g.live(pos); c != nil {
		c.removed = true
		g.removals = append(g.removals, pending{pos: pos, c: c})
	}
}

// Invalidate says the block at pos changed in a way that matters to the paths
// through it, so they are worked out again.
func (g *System) Invalidate(pos Pos) {
	if g.locked {
		return
	}
	if c := g.live(pos); c != nil {
		for i := range c.sources {
			g.schedule(c.sources[i].pos, c.sources[i].c)
		}
		return
	}
	for f := Down; f < NoFace; f++ {
		n := pos.Side(f)
		c := g.live(n)
		if c == nil {
			continue
		}
		g.schedule(n, c)
		for i := range c.sources {
			g.schedule(c.sources[i].pos, c.sources[i].c)
		}
	}
}

// ChunkLoaded works out again the paths that stopped at the chunk at origin
// because it was not loaded.
func (g *System) ChunkLoaded(origin Pos) {
	list, ok := g.waiting[origin]
	if !ok {
		return
	}
	for _, pos := range list {
		c := g.live(pos)
		if c == nil {
			continue
		}
		g.schedule(pos, c)
		for i := range c.sources {
			g.schedule(c.sources[i].pos, c.sources[i].c)
		}
	}
	delete(g.waiting, origin)
}

// schedule queues a producer or capacitor to have its paths worked out again.
// Only those start paths.
func (g *System) schedule(pos Pos, c *Component) {
	if c == nil || c.removed {
		return
	}
	if k := c.kind.group(); (k != KindProducer && k != KindCapacitor) || g.updating[pos] {
		return
	}
	g.updating[pos] = true
	g.updates = append(g.updates, pending{pos: pos, c: c})
}

// Tick counts one game tick and reports whether Evaluate is due.
func (g *System) Tick() bool {
	if g.counter >= g.Interval {
		g.counter = 1
		return true
	}
	g.counter++
	return false
}

// SignalTick reports whether block actors read what they feed the circuit this
// tick, as a comparator does what is behind it: BDS's counter is below the
// interval. Valid after Tick.
func (g *System) SignalTick() bool { return g.counter < g.Interval }

// UpdateDependencies applies the removals and additions queued since last time
// and works out the paths they changed. w may be nil, in which case no powered
// blocks are found.
func (g *System) UpdateDependencies(w World) {
	if len(g.removals) > 0 || len(g.adds) > 0 {
		g.stale = true
	}
	for _, r := range g.removals {
		g.removeNow(r.pos, r.c)
	}
	g.removals = g.removals[:0]
	for _, a := range g.adds {
		g.insert(a.pos, a.c)
	}
	g.adds = g.adds[:0]
	if len(g.updates) > 0 {
		g.applyUpdates(w)
	}
	g.evaluated = false
}

// removeNow takes c out of the graph and queues everything that may have used it.
func (g *System) removeNow(pos Pos, c *Component) {
	if g.all[pos] == c {
		delete(g.all, pos)
		g.dropOrder(c)
	}
	if c.kind != KindPowered {
		if l := g.byChunk[c.chunk]; l != nil {
			for i := range l.entries {
				if l.entries[i].c == c {
					l.entries = append(l.entries[:i], l.entries[i+1:]...)
					break
				}
			}
		}
		for _, r := range g.relation[pos] {
			if d := g.live(r.pos); d != nil {
				d.removeSourceAt(pos, c)
			}
		}
		delete(g.relation, pos)
	}
	for i := range c.sources {
		g.schedule(c.sources[i].pos, c.sources[i].c)
	}
	for len(c.dests) > 0 {
		d := c.dests[0].c
		c.dests = c.dests[1:]
		g.schedule(d.pos, d)
		d.removeSourceOfLeaving(c)
	}
	for f := Down; f < NoFace; f++ {
		if n := g.live(pos.Side(f)); n != nil {
			g.scheduleAround(n)
		}
	}
}

// removeSourceOfLeaving drops c from the lists without touching c's own counts.
func (c *Component) removeSourceOfLeaving(from *Component) {
	is := func(s *source) bool { return s.c == from }
	c.sources = keepSources(c.sources, is)
	c.side = keepSources(c.side, is)
}

// keepSources removes the entries drop says.
func keepSources(list []source, drop func(*source) bool) []source {
	kept := list[:0]
	for i := range list {
		if !drop(&list[i]) {
			kept = append(kept, list[i])
		}
	}
	clear(list[len(kept):])
	return kept
}

// scheduleAround queues d and what feeds it.
func (g *System) scheduleAround(d *Component) {
	g.schedule(d.pos, d)
	for i := range d.sources {
		g.schedule(d.sources[i].pos, d.sources[i].c)
	}
}

// scheduleNear queues every component touching pos, on its level and the levels
// above and below, and what feeds those.
func (g *System) scheduleNear(pos Pos) {
	for dy := int32(-1); dy <= 1; dy++ {
		for f := Down; f < NoFace; f++ {
			n := pos.Side(f)
			n.Y += dy
			if c := g.live(n); c != nil {
				g.scheduleAround(c)
			}
		}
	}
}

// dropOrder removes c from the list of all components, keeping the order.
func (g *System) dropOrder(c *Component) {
	for i, o := range g.order {
		if o == c {
			g.order = append(g.order[:i], g.order[i+1:]...)
			return
		}
	}
}

// insert puts a queued component in the graph. A powered block never replaces a
// real component; a real one replaces a powered block, which its users forget.
func (g *System) insert(pos Pos, c *Component) {
	if old, ok := g.all[pos]; ok {
		if old.kind != KindPowered || c.kind == KindPowered {
			return
		}
		for _, o := range g.order {
			if o.hasSource(old) {
				o.removeSourceOf(old)
			}
		}
		delete(g.all, pos)
		g.dropOrder(old)
	}
	g.all[pos] = c
	g.order = append(g.order, c)
	if c.kind == KindPowered {
		return
	}
	l := g.byChunk[c.chunk]
	if l == nil {
		l = &chunkList{origin: c.chunk, loaded: true}
		g.byChunk[c.chunk] = l
		g.chunks = append(g.chunks, l)
	}
	l.entries = append(l.entries, entry{c: c, phases: c.phases()})
	switch c.kind.group() {
	case KindProducer:
		g.schedule(pos, c)
	case KindCapacitor:
		g.schedule(pos, c)
		for _, dx := range [...]int32{-1, 1} {
			for _, dz := range [...]int32{-1, 1} {
				if d := g.live(Pos{pos.X + dx, pos.Y, pos.Z + dz}); d != nil && d.kind.group() == KindCapacitor {
					g.schedule(d.pos, d)
				}
			}
		}
	}
	g.scheduleNear(pos)
}

// applyUpdates clears the old paths of everything queued, finds the new ones and
// lets every component sort its sources.
func (g *System) applyUpdates(w World) {
	for _, u := range g.updates {
		list := g.relation[u.pos]
		for _, r := range list {
			if d := g.live(r.pos); d != nil {
				d.removeSourceAt(u.pos, u.c)
			}
		}
		g.relation[u.pos] = list[:0]
	}
	for _, u := range g.updates {
		if !u.c.removed {
			g.findRelationships(u.pos, u.c, w)
		}
		delete(g.updating, u.pos)
	}
	g.updates = g.updates[:0]
	for _, c := range g.order {
		c.updateDependencies(g)
	}
	g.stale = true
}

// findRelationships walks out from c along everything a signal can reach,
// recording c as a source wherever it arrives.
func (g *System) findRelationships(pos Pos, c *Component, w World) {
	start := slot{c: c, pos: pos, face: c.dir, kind: c.kind.group()}
	g.queue = append(g.queue[:0], tracking{current: start, origin: start, nearest: start, second: start, direct: true})
	for head := 0; head < len(g.queue); head++ {
		t := g.queue[head]
		if t.damping > g.rules.SignalReach {
			continue
		}
		if t.nearest.kind != KindPowered {
			g.findBlocks(t.nearest.pos, w)
		}
		for f := Down; f < NoFace; f++ {
			n := t.nearest.pos.Side(f)
			if n != t.second.pos {
				g.tryPath(pos, &t, g.live(n), n, f)
			}
		}
		g.searchLevels(pos, &t)
	}
	g.queue = g.queue[:0]
}

// findBlocks adds a powered component for each block next to pos that takes
// power, and remembers pos if a neighbour's chunk is not loaded yet.
func (g *System) findBlocks(pos Pos, w World) {
	if w == nil {
		return
	}
	for f := Down; f < NoFace; f++ {
		n := pos.Side(f)
		if g.live(n) != nil {
			continue
		}
		if !w.Loaded(n) {
			g.waiting[n.Chunk()] = append(g.waiting[n.Chunk()], pos)
			continue
		}
		power := w.BlockPower(n)
		if power == PowerNone {
			continue
		}
		c := newComponent(KindPowered, g.rules)
		c.pos, c.chunk, c.dir = n, n.Chunk(), NoFace
		c.first = false
		if power == PowerInsulator {
			c.allowUp, c.conducts = true, false
		}
		if old, ok := g.all[n]; ok {
			g.dropOrder(old)
		}
		g.all[n] = c
		g.order = append(g.order, c)
	}
}

// searchLevels tries the paths that change level: wire up and down past blocks,
// and from a producer standing on top of a block to the wire under that block.
func (g *System) searchLevels(start Pos, t *tracking) {
	pos := t.nearest.pos
	up, down := pos.Side(Up), pos.Side(Down)
	switch t.nearest.kind {
	case KindProducer:
		if t.nearest.c.dir != Down {
			return
		}
		above, below := g.poweredAt(up), g.poweredAt(down)
		if above == nil && below == nil {
			return
		}
		if (below != nil && (!below.conducts || below.allowUp)) ||
			(above != nil && (!above.conducts || (!above.allowUp && !above.promoted))) {
			return
		}
		g.tryWireAround(start, t, down)
	case KindWire:
		if a := g.poweredAt(up); a == nil || a.allowUp {
			g.tryWireAround(start, t, up)
		}
		if b := g.poweredAt(down); b == nil || !b.allowUp {
			g.tryWireAround(start, t, down)
		}
	}
}

// poweredAt returns the powered block component at pos, or nil.
func (g *System) poweredAt(pos Pos) *Component {
	if c := g.live(pos); c != nil && c.kind == KindPowered {
		return c
	}
	return nil
}

// tryWireAround tries wire on the four sides of centre.
func (g *System) tryWireAround(start Pos, t *tracking, centre Pos) {
	for _, f := range sides {
		n := centre.Side(f)
		if c := g.live(n); c != nil && c.kind.group() == KindWire {
			g.tryPath(start, t, c, n, f)
		}
	}
}

// tryPath offers the signal to c at pos, entered by face f. Everything offered is
// remembered, so the source can be taken back. If c lets it through, the signal
// goes on from there.
func (g *System) tryPath(start Pos, t *tracking, c *Component, pos Pos, f Face) {
	if c == nil {
		return
	}
	g.relation[start] = append(g.relation[start], related{c: c, pos: pos})
	next := *t
	next.current = slot{c: c, pos: pos, face: f, kind: c.kind.group()}
	damping, direct := t.damping, t.direct
	if !t.nearest.c.allowConnection(g, &next, &direct) || !c.addSource(&next, &damping, &direct) {
		return
	}
	next.second = t.nearest
	next.nearest = next.current
	next.damping, next.direct = damping, direct
	g.queue = append(g.queue, next)
}

// wireStep is whether wire may go from nearest to the wire at current across a
// level, which needs the block on the higher one's corner to let it past.
func (g *System) wireStep(t *tracking) bool {
	cur, near := t.current.pos, t.nearest.pos
	switch {
	case cur.Y < near.Y:
		if c := g.live(cur.Side(Up)); c != nil && !c.allowUp {
			return false
		}
		c := g.live(near.Side(Down))
		return c == nil || c.allowDown
	case cur.Y > near.Y:
		c := g.live(near.Side(Up))
		return c == nil || c.allowUp
	}
	return true
}

// dotOrLine is whether wire at pos runs nowhere, or in a line along the path it
// came in by, given by x and z.
func (g *System) dotOrLine(pos Pos, x, z int32) bool {
	n, s, e, w := g.flowsTo(pos, North), g.flowsTo(pos, South), g.flowsTo(pos, East), g.flowsTo(pos, West)
	total := 0
	for _, on := range [...]bool{n, s, e, w} {
		if on {
			total++
		}
	}
	if total == 0 {
		return true
	}
	if total != 1 {
		return false
	}
	return ((x != 0 && e) != (x != 0 && w)) || ((z != 0 && n) != (z != 0 && s))
}

// flowsTo is whether wire at pos connects to something on side f.
func (g *System) flowsTo(pos Pos, f Face) bool {
	n := pos.Side(f)
	below := false
	if c := g.live(n.Side(Down)); c != nil && g.climbable(c) {
		o := g.live(n)
		below = o == nil || o.allowUp
	}
	if c := g.live(n.Side(Up)); c != nil && g.climbable(c) {
		o := g.live(pos.Side(Up))
		if o == nil || o.allowUp || below {
			return true
		}
	} else if below {
		return true
	}
	c := g.live(n)
	if c == nil {
		return false
	}
	switch c.kind {
	case KindRepeater:
		return f == c.dir || f == c.dir.Opposite()
	case KindPulse:
		return f == c.dir.Opposite()
	}
	switch c.kind.group() {
	case KindWire, KindCapacitor, KindProducer:
		return true
	}
	return false
}

// climbable is whether c is wire another wire can connect to over a level.
func (g *System) climbable(c *Component) bool {
	return c.kind != KindRepeater && c.kind != KindPulse && c.kind.group() == KindWire
}

// Merge moves everything src holds into g, which keeps its components where both
// have one and its own place in the interval: src's circuits may evaluate a tick
// sooner or later once. src is empty afterwards. Neither may be ticking.
func (g *System) Merge(src *System) {
	g.stale, src.stale = true, true
	for _, c := range src.order {
		if _, ok := g.all[c.pos]; ok {
			continue
		}
		c.rules = g.rules
		g.all[c.pos] = c
		g.order = append(g.order, c)
	}
	for i := range src.adds {
		src.adds[i].c.rules = g.rules
	}
	for _, l := range src.chunks {
		if own, ok := g.byChunk[l.origin]; ok {
			own.entries = append(own.entries, l.entries...)
			continue
		}
		g.byChunk[l.origin] = l
		g.chunks = append(g.chunks, l)
	}
	for pos, list := range src.relation {
		g.relation[pos] = append(g.relation[pos], list...)
	}
	for pos, list := range src.waiting {
		g.waiting[pos] = append(g.waiting[pos], list...)
	}
	for pos := range src.updating {
		g.updating[pos] = true
	}
	g.adds = append(g.adds, src.adds...)
	g.updates = append(g.updates, src.updates...)
	g.removals = append(g.removals, src.removals...)
	*src = *New()
}

// RecheckWaiting works out again the paths that stopped at a chunk that can be
// read now, as after a merge brought it in. Chunks go in position order, so the
// result does not depend on the map.
func (g *System) RecheckWaiting(w World) {
	origins := make([]Pos, 0, len(g.waiting))
	for origin := range g.waiting {
		if w.Loaded(origin) {
			origins = append(origins, origin)
		}
	}
	slices.SortFunc(origins, func(a, b Pos) int {
		return cmp.Or(cmp.Compare(a.X, b.X), cmp.Compare(a.Z, b.Z))
	})
	for _, origin := range origins {
		g.ChunkLoaded(origin)
	}
}

// SplitOff moves the chunks in says into to, an empty System. Links between the
// two are cut, as the two tick apart from now on, and paths that used one are
// worked out again. Neither may be ticking.
func (g *System) SplitOff(to *System, in func(origin Pos) bool) {
	g.stale, to.stale = true, true
	to.Interval, to.counter = g.Interval, g.counter
	*to.rules = *g.rules
	moved := map[*Component]bool{}
	for _, c := range g.order {
		if in(c.chunk) {
			moved[c] = true
			c.rules = to.rules
			to.all[c.pos] = c
			to.order = append(to.order, c)
			delete(g.all, c.pos)
		}
	}
	g.order = slices.DeleteFunc(g.order, func(c *Component) bool { return moved[c] })
	for _, l := range g.chunks {
		if in(l.origin) {
			to.byChunk[l.origin] = l
			to.chunks = append(to.chunks, l)
			delete(g.byChunk, l.origin)
		}
	}
	g.chunks = slices.DeleteFunc(g.chunks, func(l *chunkList) bool { return in(l.origin) })
	for pos, list := range g.relation {
		if in(pos.Chunk()) {
			to.relation[pos] = list
			delete(g.relation, pos)
		}
	}
	for origin, list := range g.waiting {
		if in(origin) {
			to.waiting[origin] = list
			delete(g.waiting, origin)
		}
	}
	movePending := func(list []pending) (kept, gone []pending) {
		for _, p := range list {
			if in(p.pos.Chunk()) {
				gone = append(gone, p)
			} else {
				kept = append(kept, p)
			}
		}
		return kept, gone
	}
	g.adds, to.adds = movePending(g.adds)
	for i := range to.adds {
		to.adds[i].c.rules = to.rules
	}
	g.removals, to.removals = movePending(g.removals)
	g.updates, to.updates = movePending(g.updates)
	for _, p := range to.updates {
		to.updating[p.pos] = true
		delete(g.updating, p.pos)
	}
	cutLinks(g, moved, false)
	cutLinks(to, moved, true)
}

// cutLinks drops the sources of g's components that now live in the other System,
// and queues what they fed to find its paths again. isMoved says which side g is.
func cutLinks(g *System, moved map[*Component]bool, isMoved bool) {
	for _, c := range g.order {
		other := func(s *source) bool { return moved[s.c] != isMoved }
		if !slices.ContainsFunc(c.sources, func(s source) bool { return other(&s) }) &&
			!slices.ContainsFunc(c.side, func(s source) bool { return other(&s) }) {
			continue
		}
		c.sources = c.dropSources(c.sources, other)
		c.side = c.dropSources(c.side, other)
		g.scheduleAround(c)
	}
}

// Evaluate runs one round of every loaded chunk's components, in BDS's phases:
// all inputs are read, then producers and capacitors change, then everything
// else, then repeaters lock. Chunks run in the order their first component came.
func (g *System) Evaluate(w World) { g.EvaluateParallel(w, nil) }

// Runner calls fn(i) once for each i in [0, n), on any goroutines, and returns once every
// call has. Helix passes its Region.Parallel.
type Runner func(n int, fn func(i int))

// EvaluateParallel is Evaluate with the circuits that share nothing (islands) evaluated by
// run, so an engine can spread them over cores. Each island still runs BDS's four phases
// over its components in the order Evaluate would, and no island reads or changes another,
// so the result is the same as Evaluate's on any number of cores. A nil run is Evaluate.
func (g *System) EvaluateParallel(w World, run Runner) {
	for _, l := range g.chunks {
		l.loaded = w == nil || w.ChunksLoaded(l.origin, g.rules.LoadRadius)
	}
	g.evaluated = true
	if run == nil {
		g.runPhase(phaseCache)
		g.runPhase(phaseProducer)
		g.runPhase(phaseOther)
		g.runPhase(phaseLock)
		return
	}
	if g.stale {
		g.findIslands()
	}
	if g.islandFn == nil {
		g.islandFn = g.evaluateIsland
	}
	run(len(g.islands), g.islandFn)
}

// runPhase runs one phase over all chunks.
func (g *System) runPhase(phase uint8) {
	for _, l := range g.chunks {
		if !l.loaded {
			continue
		}
		for i := range l.entries {
			runEntry(&l.entries[i], phase)
		}
	}
}

// runEntry runs one phase of one component.
func runEntry(e *entry, phase uint8) {
	if e.phases&phase == 0 || e.c.removed {
		return
	}
	switch phase {
	case phaseCache:
		e.c.cache()
	case phaseLock:
		e.c.checkLock()
	default:
		if e.c.evaluate() {
			e.c.needsUpdate = true
		}
	}
}

// island is a circuit no other reaches: its components, in Evaluate's order.
type island struct {
	items []islandItem
}

// islandItem is one component of an island: entry i of chunk list l.
type islandItem struct {
	l *chunkList
	i int
}

// evaluateIsland runs the four phases over island i.
func (g *System) evaluateIsland(i int) {
	items := g.islands[i].items
	for _, phase := range [...]uint8{phaseCache, phaseProducer, phaseOther, phaseLock} {
		for _, it := range items {
			if it.l.loaded {
				runEntry(&it.l.entries[it.i], phase)
			}
		}
	}
}

// findIslands splits the components into islands: those linked as sources, sides,
// destinations or torch rings share one. Islands and their components keep Evaluate's
// order (chunks in order, components in each chunk's order), so the split is the same
// every time.
func (g *System) findIslands() {
	g.stale = false
	index := map[*Component]int{}
	var at []islandItem
	for _, l := range g.chunks {
		for i := range l.entries {
			index[l.entries[i].c] = len(at)
			at = append(at, islandItem{l: l, i: i})
		}
	}
	parent := make([]int, len(at))
	for i := range parent {
		parent[i] = i
	}
	var find func(i int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	join := func(a int, b *Component) {
		j, ok := index[b]
		if !ok {
			return
		}
		ra, rb := find(a), find(j)
		if ra != rb {
			parent[max(ra, rb)] = min(ra, rb)
		}
	}
	for i, it := range at {
		c := it.l.entries[it.i].c
		for k := range c.sources {
			join(i, c.sources[k].c)
		}
		for k := range c.side {
			join(i, c.side[k].c)
		}
		for k := range c.dests {
			join(i, c.dests[k].c)
		}
		if c.chain != nil {
			join(i, c.chain)
		}
	}
	clear(g.islands)
	g.islands = g.islands[:0]
	of := map[int]int{}
	for i, it := range at {
		root := find(i)
		n, ok := of[root]
		if !ok {
			n = len(g.islands)
			of[root] = n
			g.islands = append(g.islands, island{})
		}
		g.islands[n].items = append(g.islands[n].items, it)
	}
}

// UpdateChunk tells the block of every component in the chunk at origin that
// changed in the last Evaluate. Consumers that ended up directly powered go
// last, so they see what the others did.
func (g *System) UpdateChunk(origin Pos, w World) {
	if !g.evaluated {
		return
	}
	l := g.byChunk[origin]
	if l == nil {
		return
	}
	var later []*Component
	for i := 0; i < len(l.entries); i++ {
		c := l.entries[i].c
		if !c.needsUpdate || c.removed {
			continue
		}
		c.needsUpdate = false
		if c.kind == KindConsumer && c.directlyPowered {
			later = append(later, c)
			continue
		}
		g.report(c, w)
	}
	for _, c := range later {
		g.report(c, w)
	}
}

// report hands a component's change to the engine.
func (g *System) report(c *Component, w World) {
	s := c.Strength()
	old := c.previous
	if c.kind != KindComparator {
		c.previous = s
	}
	if s == -1 {
		return
	}
	if !c.first || !c.ignoreFirst {
		w.PowerChanged(c.pos, s, old, c.first)
	}
	c.first = false
}
