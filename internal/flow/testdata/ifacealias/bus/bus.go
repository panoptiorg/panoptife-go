package bus

// Event carries a payload through an interface-typed field: one heap cell.
type Event struct{ Payload any }

var Ch = make(chan *Event, 1)
