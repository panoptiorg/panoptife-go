package ledger

import "os/exec"

// INotifier has 12 impls — over the fan-out cap (10) — so the dispatch site in
// notifyAll stays capped-opaque and the exec sink hidden in Notifier07.Send
// must never surface as a chain, in any mode.
type INotifier interface {
	Send(msg string) string
}

type Notifier01 struct{}

func (Notifier01) Send(msg string) string { return "01:" + msg }

type Notifier02 struct{}

func (Notifier02) Send(msg string) string { return "02:" + msg }

type Notifier03 struct{}

func (Notifier03) Send(msg string) string { return "03:" + msg }

type Notifier04 struct{}

func (Notifier04) Send(msg string) string { return "04:" + msg }

type Notifier05 struct{}

func (Notifier05) Send(msg string) string { return "05:" + msg }

type Notifier06 struct{}

func (Notifier06) Send(msg string) string { return "06:" + msg }

type Notifier07 struct{}

func (Notifier07) Send(msg string) string {
	_ = exec.Command(msg)
	return "07:" + msg
}

type Notifier08 struct{}

func (Notifier08) Send(msg string) string { return "08:" + msg }

type Notifier09 struct{}

func (Notifier09) Send(msg string) string { return "09:" + msg }

type Notifier10 struct{}

func (Notifier10) Send(msg string) string { return "10:" + msg }

type Notifier11 struct{}

func (Notifier11) Send(msg string) string { return "11:" + msg }

type Notifier12 struct{}

func (Notifier12) Send(msg string) string { return "12:" + msg }
