package state

// A chat's boost is how its conversation reads, as Zen's Boosts restyle one
// site: a face from a short list, a text size, and a colour its own
// bubbles take a little of. It is the person's, per chat and per account,
// kept in the account's settings file; no one else sees it.

// ChatBoost is a chat's boost; the zero one is none.
type ChatBoost struct {
	Font string  // BoostFonts; "" the system's
	Size float32 // BoostSizes; 0 is 1
	Tint string  // SpaceTints: the colour your bubbles take; "" none
}

// BoostFonts are the faces a chat can read in: the system's, a serif, a
// humanist sans and the system's monospace (theme.BoostFace names them).
var BoostFonts = []string{"", "serif", "humanist", "mono"}

// BoostSizes are the text sizes a chat can read at, times the usual, as
// Zen's zoom steps.
var BoostSizes = []float32{0.9, 1, 1.1, 1.25, 1.5}

// Scale is how much larger the boosted chat's text is (1 the usual).
func (b ChatBoost) Scale() float32 {
	if b.Size == 0 {
		return 1
	}
	return b.Size
}

// BoostOf is the boost of chat jid on a (none when it has none).
func (a *Account) BoostOf(jid []byte) ChatBoost {
	return a.Settings.Boosts[string(jid)]
}

// SetBoost gives chat jid on a boost b (none takes it away) and saves it.
func (a *Account) SetBoost(jid []byte, b ChatBoost) {
	if len(jid) == 0 {
		return
	}
	b = b.valid()
	if b == (ChatBoost{}) {
		if _, ok := a.Settings.Boosts[string(jid)]; !ok {
			return
		}
		delete(a.Settings.Boosts, string(jid))
	} else {
		if a.Settings.Boosts == nil {
			a.Settings.Boosts = map[string]ChatBoost{}
		}
		a.Settings.Boosts[string(jid)] = b
	}
	a.SettingsChanged()
}

// valid is b with what is not one of the choices left out.
func (b ChatBoost) valid() ChatBoost {
	in := func(s string, of []string) bool {
		for _, v := range of {
			if v == s {
				return true
			}
		}
		return false
	}
	if !in(b.Font, BoostFonts) {
		b.Font = ""
	}
	if b.Tint != "" && !in(b.Tint, SpaceTints) {
		b.Tint = ""
	}
	ok := false
	for _, v := range BoostSizes {
		ok = ok || v == b.Size
	}
	if !ok || b.Size == 1 {
		b.Size = 0
	}
	return b
}
