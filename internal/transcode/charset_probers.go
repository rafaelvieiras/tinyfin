package transcode

// Probers of the charset detector (bitmap.charset.R7-R17).

type csProber interface {
	feed(buf []byte) int
	confidence() float32
	name() string
}

// csSM is a running coding state machine (R7).
type csSM struct {
	m      *csMachine
	state  int
	curLen int
}

func (s *csSM) next(b byte) int {
	c := int(s.m.cls[b])
	if s.state == 0 {
		s.curLen = int(s.m.charLen[c])
	}
	s.state = int(s.m.states[s.state*s.m.classFactor+c])
	return s.state
}

// csGroup runs members over each chunk (R9, R13).
type csGroup struct {
	members []csProber
	active  []bool
	nActive int
	state   int
	best    int
	filter  func([]byte) []byte
}

func newCSGroup(m []csProber, filter func([]byte) []byte) *csGroup {
	g := &csGroup{members: m, active: make([]bool, len(m)), nActive: len(m), best: -1, filter: filter}
	for i := range g.active {
		g.active[i] = true
	}
	return g
}

func (g *csGroup) feed(chunk []byte) int {
	buf := g.filter(chunk)
	if len(buf) == 0 {
		return g.state
	}
	for i, m := range g.members {
		if !g.active[i] {
			continue
		}
		switch m.feed(buf) {
		case stFound:
			g.state, g.best = stFound, i
			return g.state
		case stNotMe:
			g.active[i] = false
			g.nActive--
			if g.nActive <= 0 {
				g.state = stNotMe
				return g.state
			}
		}
	}
	return g.state
}

func (g *csGroup) confidence() float32 {
	switch g.state {
	case stFound:
		return csSureYes
	case stNotMe:
		return csSureNo
	}
	var bestConf float32
	for i, m := range g.members {
		if !g.active[i] {
			continue
		}
		if c := m.confidence(); bestConf < c {
			bestConf, g.best = c, i
		}
	}
	return bestConf
}

func (g *csGroup) name() string {
	if g.best == -1 {
		g.confidence()
		if g.best == -1 {
			g.best = 0
		}
	}
	return g.members[g.best].name()
}

// --- UTF-8 (R8) ---

type csUTF8 struct {
	sm    csSM
	n     int
	state int
}

func (p *csUTF8) feed(buf []byte) int {
	for _, b := range buf {
		switch p.sm.next(b) {
		case 1:
			p.state = stNotMe
			return p.state
		case 2:
			p.state = stFound
			return p.state
		case 0:
			if p.sm.curLen >= 2 {
				p.n++
			}
		}
	}
	if p.state == stDetecting && p.confidence() > csShortcut {
		p.state = stFound
	}
	return p.state
}

func (p *csUTF8) confidence() float32 {
	if p.n < 6 {
		unlike := float32(0.99)
		for i := 0; i < p.n; i++ {
			unlike = float32(unlike * float32(0.5))
		}
		return float32(1 - unlike)
	}
	return csSureYes
}

func (p *csUTF8) name() string { return "utf-8" }

// --- CJK (R10-R12) ---

const (
	kSJIS = iota
	kEUCJP
	kGB
	kKR
	kBig5
	kTW
)

type csCJK struct {
	nm       string
	kind     int
	sm       csSM
	dist     *csDist
	total    int
	freq     int
	jp       [][]uint8
	pairs    int
	rel      [6]int
	lastOrd  int
	done     bool
	lastByte byte
	state    int
}

func (p *csCJK) order(b0, b1 byte) int {
	x, y := int(b0), int(b1)
	switch p.kind {
	case kSJIS:
		var o int
		switch {
		case x >= 0x81 && x <= 0x9F:
			o = 188 * (x - 0x81)
		case x >= 0xE0 && x <= 0xEF:
			o = 188 * (x - 0xE0 + 31)
		default:
			return -1
		}
		o += y - 0x40
		if y > 0x7F {
			o--
		}
		return o
	case kEUCJP:
		if x >= 0xA0 {
			return 94*(x-0xA1) + y - 0xA1
		}
	case kGB:
		if x >= 0xB0 && y >= 0xA1 {
			return 94*(x-0xB0) + y - 0xA1
		}
	case kKR:
		if x >= 0xB0 {
			return 94*(x-0xB0) + y - 0xA1
		}
	case kBig5:
		if x >= 0xA4 {
			if y >= 0xA1 {
				return 157*(x-0xA4) + (y - 0xA1 + 63)
			}
			return 157*(x-0xA4) + (y - 0x40)
		}
	case kTW:
		if x >= 0xC4 {
			return 94*(x-0xC4) + y - 0xA1
		}
	}
	return -1
}

func (p *csCJK) handleDist(b0, b1 byte) {
	o := p.order(b0, b1)
	if o < 0 {
		return
	}
	p.total++
	if o < len(p.dist.frequent) && p.dist.frequent[o] {
		p.freq++
	}
}

func (p *csCJK) hiragana(b0, b1 byte) int {
	if p.kind == kSJIS {
		if b0 == 0x82 && b1 >= 0x9F && b1 <= 0xF1 {
			return int(b1) - 0x9F
		}
	} else if b0 == 0xA4 && b1 >= 0xA1 && b1 <= 0xF3 {
		return int(b1) - 0xA1
	}
	return -1
}

func (p *csCJK) handleJP(b0, b1 byte, n int) {
	if p.pairs > 1000 {
		p.done = true
	}
	if p.done {
		return
	}
	o := -1
	if n == 2 {
		o = p.hiragana(b0, b1)
	}
	if o != -1 && p.lastOrd != -1 {
		p.pairs++
		p.rel[p.jp[p.lastOrd][o]]++
	}
	p.lastOrd = o
}

func (p *csCJK) feed(buf []byte) int {
	for i, b := range buf {
		switch p.sm.next(b) {
		case 1:
			p.state = stNotMe
			return p.state
		case 2:
			p.state = stFound
			return p.state
		case 0:
			b0 := p.lastByte
			if i > 0 {
				b0 = buf[i-1]
			}
			if p.jp != nil {
				p.handleJP(b0, b, p.sm.curLen)
			}
			if p.sm.curLen == 2 {
				p.handleDist(b0, b)
			}
		}
	}
	p.lastByte = buf[len(buf)-1]
	if p.state == stDetecting {
		enough := p.total > 1024
		if p.jp != nil {
			enough = p.pairs > 100
		}
		if enough && p.confidence() > csShortcut {
			p.state = stFound
		}
	}
	return p.state
}

func (p *csCJK) distConfidence() float32 {
	if p.total <= 0 || p.freq <= 4 {
		return csSureNo
	}
	if p.total != p.freq {
		r := float32(float32(p.freq) / float32(float32(p.total-p.freq)*p.dist.ratio))
		if r < csSureYes {
			return r
		}
	}
	return csSureYes
}

func (p *csCJK) confidence() float32 {
	c := p.distConfidence()
	if p.jp != nil {
		j := float32(-1)
		if p.pairs > 4 {
			j = float32(float32(p.pairs-p.rel[0]) / float32(p.pairs))
		}
		if j > c {
			c = j
		}
	}
	return c
}

func (p *csCJK) name() string { return p.nm }

func csMBFilter(chunk []byte) []byte {
	out := make([]byte, 0, len(chunk))
	keep := true
	for _, b := range chunk {
		if b&0x80 != 0 {
			out = append(out, b)
			keep = true
		} else if keep {
			out = append(out, b)
			keep = false
		}
	}
	return out
}

func newMBGroup(t *csTabs) *csGroup {
	mk := func(name string, kind int, table string) csProber {
		p := &csCJK{nm: name, kind: kind, sm: csSM{m: t.machines[name]}, dist: t.dist[table], lastOrd: -1}
		if kind == kSJIS || kind == kEUCJP {
			p.jp = t.jp
		}
		return p
	}
	return newCSGroup([]csProber{
		&csUTF8{sm: csSM{m: t.machines["utf-8"]}},
		mk("shift-jis", kSJIS, "sjis_eucjp"),
		mk("euc-jp", kEUCJP, "sjis_eucjp"),
		mk("gb18030", kGB, "gb2312"),
		mk("euc-kr", kKR, "euckr"),
		mk("cp949", kKR, "euckr"),
		mk("big5", kBig5, "big5"),
		mk("euc-tw", kTW, "euctw"),
	}, csMBFilter)
}

// --- single-byte (R13-R15) ---

type csSB struct {
	spec       *csProberSpec
	state      int
	totalChar  int
	ctrl       int
	freq       int
	totalSeqs  int
	seq        [4]int
	lastOrder  int
	hebrew     *csHebrew
	logical    *csSB
	visual     *csSB
	isHebrewNm bool
}

func (p *csSB) feed(buf []byte) int {
	l := p.spec.lang
	fc := l.freqCharCount
	for _, b := range buf {
		o := int(p.spec.order[b])
		if o < 250 {
			p.totalChar++
		} else if o == 255 {
			p.state = stNotMe
			break
		} else if o == 254 {
			p.ctrl++
		}
		if o < fc {
			p.freq++
			if p.lastOrder < fc {
				p.totalSeqs++
				var cat uint8
				if p.spec.reversed {
					cat = l.matrix[o*fc+p.lastOrder]
				} else {
					cat = l.matrix[p.lastOrder*fc+o]
				}
				p.seq[cat]++
			}
		}
		p.lastOrder = o
	}
	if p.state == stDetecting && p.totalSeqs > 1024 {
		c := p.confidence()
		if c > csShortcut {
			p.state = stFound
		} else if c < 0.05 {
			p.state = stNotMe
		}
	}
	return p.state
}

func (p *csSB) confidence() float32 {
	if p.totalSeqs <= 0 {
		return csSureNo
	}
	tc := float32(p.totalChar)
	r := float32(float32(p.seq[3]) / float32(p.totalSeqs))
	r = float32(r / p.spec.lang.typical)
	r = float32(float32(r*float32(float32(p.seq[3])+float32(float32(p.seq[2])/4))) / tc)
	r = float32(float32(r*float32(p.totalChar-p.ctrl)) / tc)
	r = float32(float32(r*float32(p.freq)) / tc)
	if r >= 1 {
		return csSureYes
	}
	return r
}

func (p *csSB) name() string {
	if p.spec.nameProber {
		return p.hebrew.pickName(p.logical, p.visual)
	}
	return p.spec.name
}

// csHebrew is the helper member (R15).
type csHebrew struct {
	prev, beforePrev byte
	logical, visual  int
	m11, m12         *csSB
}

func csFinal(b byte) bool    { return b == 0xEA || b == 0xED || b == 0xEF || b == 0xF3 || b == 0xF5 }
func csNonFinal(b byte) bool { return b == 0xEB || b == 0xEE || b == 0xF0 || b == 0xF4 }

func (h *csHebrew) feed(buf []byte) int {
	if h.m11.state == stNotMe && h.m12.state == stNotMe {
		return stNotMe
	}
	for _, b := range buf {
		if b == ' ' {
			if h.beforePrev != ' ' {
				if csFinal(h.prev) {
					h.logical++
				} else if csNonFinal(h.prev) {
					h.visual++
				}
			}
		} else if h.beforePrev == ' ' && csFinal(h.prev) {
			h.visual++
		}
		h.beforePrev, h.prev = h.prev, b
	}
	return stDetecting
}

func (h *csHebrew) confidence() float32 { return 0 }
func (h *csHebrew) name() string        { return "" }

func (h *csHebrew) pickName(logical, visual *csSB) string {
	d := h.logical - h.visual
	if d >= 5 {
		return "windows-1255"
	}
	if d <= -5 {
		return "iso-8859-8"
	}
	m := float32(logical.confidence() - visual.confidence())
	if m > 0.01 {
		return "windows-1255"
	}
	if m < -0.01 {
		return "iso-8859-8"
	}
	if d < 0 {
		return "iso-8859-8"
	}
	return "windows-1255"
}

// csSBFilter is R13a.
func csSBFilter(chunk []byte) []byte {
	var out []byte
	start := 0
	sawHigh := false
	for i, b := range chunk {
		if b&0x80 != 0 {
			sawHigh = true
			continue
		}
		if csIsLetter(b) {
			continue
		}
		if sawHigh && i > start {
			out = append(out, chunk[start:i]...)
			out = append(out, ' ')
			sawHigh = false
		}
		start = i + 1
	}
	if sawHigh && len(chunk) > start {
		out = append(out, chunk[start:]...)
	}
	return out
}

func newSBGroup(t *csTabs) *csGroup {
	ms := make([]csProber, len(t.probers))
	var helper *csHebrew
	var m11, m12 *csSB
	for i := range t.probers {
		sp := &t.probers[i]
		if sp.hebrewHelper {
			helper = &csHebrew{prev: ' ', beforePrev: ' '}
			ms[i] = helper
			continue
		}
		m := &csSB{spec: sp, lastOrder: 255}
		ms[i] = m
		if sp.nameProber {
			if sp.reversed {
				m12 = m
			} else {
				m11 = m
			}
		}
	}
	if helper != nil {
		helper.m11, helper.m12 = m11, m12
		for _, m := range []*csSB{m11, m12} {
			m.hebrew, m.logical, m.visual = helper, m11, m12
		}
	}
	return newCSGroup(ms, csSBFilter)
}

// --- Latin-1 (R16) ---

const (
	lcUDF = iota
	lcOTH
	lcASC
	lcASS
	lcACV
	lcACO
	lcASV
	lcASO
)

var csLatinModel = [8][8]uint8{
	{0, 0, 0, 0, 0, 0, 0, 0},
	{0, 3, 3, 3, 3, 3, 3, 3},
	{0, 3, 3, 3, 3, 3, 3, 3},
	{0, 3, 3, 3, 1, 1, 3, 3},
	{0, 3, 3, 3, 1, 2, 1, 2},
	{0, 3, 3, 3, 3, 3, 3, 3},
	{0, 3, 1, 3, 1, 1, 1, 3},
	{0, 3, 1, 3, 1, 1, 3, 3},
}

func csLatinClass(b byte) int {
	switch {
	case b >= 'A' && b <= 'Z':
		return lcASC
	case b >= 'a' && b <= 'z':
		return lcASS
	case b < 0x80:
		return lcOTH
	case b < 0xA0:
		switch b {
		case 0x83, 0x9A, 0x9C, 0x9E:
			return lcASO
		case 0x8A, 0x8C, 0x8E, 0x9F:
			return lcACO
		case 0x81, 0x8D, 0x8F, 0x90, 0x9D:
			return lcUDF
		}
		return lcOTH
	case b < 0xC0:
		return lcOTH
	case b < 0xE0:
		switch {
		case b == 0xC6 || b == 0xC7 || b == 0xD0 || b == 0xD1 || b >= 0xDD:
			return lcACO
		case b == 0xD7:
			return lcOTH
		}
		return lcACV
	}
	switch {
	case b == 0xE6 || b == 0xE7 || b == 0xF0 || b == 0xF1 || b >= 0xFD:
		return lcASO
	case b == 0xF7:
		return lcOTH
	}
	return lcASV
}

type csLatin1 struct {
	lastClass int
	counter   [4]int
	notMe     bool
}

func (p *csLatin1) feed(chunk []byte) {
	var buf []byte
	start := 0
	inTag := false
	for i, b := range chunk {
		if b == '>' {
			inTag = false
		} else if b == '<' {
			inTag = true
		}
		if b < 0x80 && !csIsLetter(b) {
			if i > start && !inTag {
				buf = append(buf, chunk[start:i]...)
				buf = append(buf, ' ')
			}
			start = i + 1
		}
	}
	if !inTag {
		buf = append(buf, chunk[start:]...)
	}
	for _, b := range buf {
		c := csLatinClass(b)
		f := csLatinModel[p.lastClass][c]
		if f == 0 {
			p.notMe = true
			break
		}
		p.counter[f]++
		p.lastClass = c
	}
}

func (p *csLatin1) confidence() float32 {
	if p.notMe {
		return csSureNo
	}
	total := p.counter[0] + p.counter[1] + p.counter[2] + p.counter[3]
	if total == 0 {
		return 0
	}
	t := float32(total)
	c := float32(float32(float32(p.counter[3])/t) - float32(float32(float32(p.counter[1])*20)/t))
	if c < 0 {
		return 0
	}
	return float32(c * 0.5)
}

func (p *csLatin1) name() string { return "windows-1252" }

// --- escape sequences (R17) ---

type csEscape struct {
	sms   []*csSM
	names []string
	n     int
}

func newEscape(t *csTabs) *csEscape {
	e := &csEscape{}
	for _, n := range []string{"hz-gb-2312", "iso-2022-cn", "iso-2022-jp", "iso-2022-kr"} {
		e.sms = append(e.sms, &csSM{m: t.machines[n]})
		e.names = append(e.names, n)
	}
	e.n = len(e.sms)
	return e
}

func (e *csEscape) feed(chunk []byte) string {
	for _, b := range chunk {
		for i := e.n - 1; i >= 0; i-- {
			switch e.sms[i].next(b) {
			case 1:
				e.n--
				e.sms[i], e.sms[e.n] = e.sms[e.n], e.sms[i]
				e.names[i], e.names[e.n] = e.names[e.n], e.names[i]
				if e.n <= 0 {
					return ""
				}
			case 2:
				return e.names[i]
			}
		}
	}
	return ""
}
