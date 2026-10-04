package containers

func sink(string) {}

type holder struct{ m map[string]string }

func MapWrite(q string) {
	m := map[string]string{}
	m["k"] = q
	sink(m["k"])
}

func MapKey(q string) {
	m := map[string]bool{}
	m[q] = true
	for k := range m {
		sink(k)
	}
}

func MapInField(q string) {
	c := &holder{m: map[string]string{}}
	c.m["k"] = q
	sink(c.m["k"])
}

func ChanSend(q string) {
	ch := make(chan string, 1)
	ch <- q
	sink(<-ch)
}

func SelectSend(q string) {
	ch := make(chan string, 1)
	select {
	case ch <- q:
	default:
	}
	sink(<-ch)
}

func OtherMap(q string) {
	a := map[string]string{}
	a["k"] = q
	b := map[string]string{"k": "x"}
	sink(b["k"])
}

func OtherChan(q string) {
	a := make(chan string, 1)
	b := make(chan string, 1)
	a <- q
	b <- "x"
	sink(<-b)
}

func Produce(ch chan string, q string) { ch <- q }
