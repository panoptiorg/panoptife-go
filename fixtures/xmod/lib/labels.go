package lib

// Labeler has more implementations here than a call site can list: eleven,
// one over the fan-out cap.
type Labeler interface {
	Label() string
}

type l0 struct{}
type l1 struct{}
type l2 struct{}
type l3 struct{}
type l4 struct{}
type l5 struct{}
type l6 struct{}
type l7 struct{}
type l8 struct{}
type l9 struct{}
type l10 struct{}

func (l0) Label() string  { return "0" }
func (l1) Label() string  { return "1" }
func (l2) Label() string  { return "2" }
func (l3) Label() string  { return "3" }
func (l4) Label() string  { return "4" }
func (l5) Label() string  { return "5" }
func (l6) Label() string  { return "6" }
func (l7) Label() string  { return "7" }
func (l8) Label() string  { return "8" }
func (l9) Label() string  { return "9" }
func (l10) Label() string { return "10" }

// Pick returns the Labeler for n.
func Pick(n int) Labeler {
	all := []Labeler{l0{}, l1{}, l2{}, l3{}, l4{}, l5{}, l6{}, l7{}, l8{}, l9{}, l10{}}
	return all[n%len(all)]
}
