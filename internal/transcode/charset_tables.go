package transcode

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// Provenance: the three files are the data tables of the charset detector
// (docs/contract/data/charset-*.json); each carries its own "provenance" field.

//go:embed chardata/charset-statemachines.json
var csStateMachinesJSON []byte

//go:embed chardata/charset-cjk.json
var csCJKJSON []byte

//go:embed chardata/charset-sbcs.json
var csSBCSJSON []byte

type csMachine struct {
	classFactor int
	cls         [256]uint8
	states      []uint8
	charLen     []uint8
}

type csDist struct {
	ratio    float32
	frequent []bool // indexed by order, length tableLength
}

type csLang struct {
	freqCharCount int
	typical       float32
	matrix        []uint8
}

type csProberSpec struct {
	hebrewHelper bool
	lang         *csLang
	name         string
	order        [256]uint8
	reversed     bool
	nameProber   bool
}

type csTabs struct {
	machines map[string]*csMachine
	dist     map[string]*csDist
	jp       [][]uint8
	probers  []csProberSpec
}

var csTables = sync.OnceValue(func() *csTabs {
	t := &csTabs{machines: map[string]*csMachine{}, dist: map[string]*csDist{}}
	var sm struct {
		Machines map[string]struct {
			ClassFactor int   `json:"classFactor"`
			Cls         []int `json:"cls"`
			States      []int `json:"states"`
			CharLen     []int `json:"charLen"`
		} `json:"machines"`
	}
	mustJSON(csStateMachinesJSON, &sm)
	for name, m := range sm.Machines {
		cm := &csMachine{classFactor: m.ClassFactor}
		for i, v := range m.Cls {
			cm.cls[i] = uint8(v)
		}
		for _, v := range m.States {
			cm.states = append(cm.states, uint8(v))
		}
		for _, v := range m.CharLen {
			cm.charLen = append(cm.charLen, uint8(v))
		}
		t.machines[name] = cm
	}
	var cjk struct {
		Distribution map[string]struct {
			TableLength  int     `json:"tableLength"`
			Ratio        float64 `json:"typicalDistributionRatio"`
			FrequentOrds []int   `json:"frequentOrders"`
		} `json:"distribution"`
		JPContext [][]int `json:"jpContext"`
	}
	mustJSON(csCJKJSON, &cjk)
	for name, d := range cjk.Distribution {
		cd := &csDist{ratio: float32(d.Ratio), frequent: make([]bool, d.TableLength)}
		for _, o := range d.FrequentOrds {
			if o >= 0 && o < d.TableLength {
				cd.frequent[o] = true
			}
		}
		t.dist[name] = cd
	}
	for _, row := range cjk.JPContext {
		r := make([]uint8, len(row))
		for i, v := range row {
			r[i] = uint8(v)
		}
		t.jp = append(t.jp, r)
	}
	var sb struct {
		Languages map[string]struct {
			FreqCharCount int     `json:"freqCharCount"`
			Typical       float64 `json:"typicalPositiveRatio"`
			Matrix        string  `json:"matrix"`
		} `json:"languages"`
		Probers []struct {
			Kind       string `json:"kind"`
			Lang       string `json:"lang"`
			Name       string `json:"name"`
			Order      []int  `json:"order"`
			Reversed   bool   `json:"reversed"`
			NameProber bool   `json:"nameProber"`
		} `json:"probers"`
	}
	mustJSON(csSBCSJSON, &sb)
	langs := map[string]*csLang{}
	for name, l := range sb.Languages {
		cl := &csLang{freqCharCount: l.FreqCharCount, typical: float32(l.Typical), matrix: make([]uint8, len(l.Matrix))}
		for i := range l.Matrix {
			cl.matrix[i] = l.Matrix[i] - '0'
		}
		langs[name] = cl
	}
	for _, p := range sb.Probers {
		sp := csProberSpec{hebrewHelper: p.Kind == "hebrew", name: p.Name, reversed: p.Reversed, nameProber: p.NameProber}
		if !sp.hebrewHelper {
			sp.lang = langs[p.Lang]
			for i, v := range p.Order {
				sp.order[i] = uint8(v)
			}
		}
		t.probers = append(t.probers, sp)
	}
	return t
})

func mustJSON(b []byte, v any) {
	if err := json.Unmarshal(b, v); err != nil {
		panic("charset tables: " + err.Error())
	}
}
