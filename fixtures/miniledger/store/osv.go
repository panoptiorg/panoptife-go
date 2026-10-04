package store

func (s *Storage) SaveOsv(row string) error {
	return s.db.Execx("INSERT INTO osv VALUES (" + row + ")")
}

func (s *Storage) GetOsv(id string) (string, error) {
	return s.db.Getx("SELECT * FROM osv WHERE id = " + id)
}

func (s *Storage) ListOsvParts(id string) ([]string, error) {
	return s.db.Selectx("SELECT * FROM osv_parts WHERE osv_id = " + id)
}

func (s *Storage) ReviewOsv(id string) error {
	return s.db.Execx("UPDATE osv SET reviewed = true WHERE id = " + id)
}

func (s *Storage) BackupOsv(id string) ([]string, error) {
	return s.db.Queryx("INSERT INTO osv_backup SELECT * FROM osv WHERE id = " + id)
}
