package store

func (s *Storage) FetchAccount(number string) (string, error) {
	return s.db.Getx("SELECT * FROM accounts WHERE number = " + number)
}

func (s *Storage) ListAccounts(owner string) ([]string, error) {
	return s.db.Selectx("SELECT * FROM accounts WHERE owner = " + owner)
}

func (s *Storage) SaveAccount(row string) error {
	return s.db.Execx("INSERT INTO accounts VALUES (" + row + ")")
}

func (s *Storage) DeleteAccount(number string) error {
	return s.db.Execx("DELETE FROM accounts WHERE number = " + number)
}

func (s *Storage) SearchAccounts(filter string) ([]string, error) {
	return s.db.Queryx("SELECT * FROM accounts WHERE " + filter)
}

func (s *Storage) CountAccounts(owner string) (int, error) {
	return s.db.Getxx("SELECT count(*) FROM accounts WHERE owner = " + owner)
}
