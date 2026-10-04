package store

func (s *Storage) SaveReserve(row string) error {
	return s.db.Execx("INSERT INTO reserves VALUES (" + row + ")")
}

func (s *Storage) GetReserve(id string) (string, error) {
	return s.db.Getx("SELECT * FROM reserves WHERE id = " + id)
}

func (s *Storage) ListReserves(day string) ([]string, error) {
	return s.db.Selectx("SELECT * FROM reserves WHERE day = " + day)
}

func (s *Storage) RecalcReserve(id string) error {
	return s.db.Execx("UPDATE reserves SET amount = recalc(" + id + ")")
}

func (s *Storage) ReserveHistory(id string) ([]string, error) {
	return s.db.Queryx("SELECT * FROM reserve_log WHERE id = " + id)
}

func (s *Storage) AccrueReserve(day string) error {
	return s.db.Execx("UPDATE reserves SET accrued = true WHERE day = " + day)
}
