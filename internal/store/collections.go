package store

import "time"

type Collection struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	Sort  int    `json:"sort"`
	Count int    `json:"count"`
}

// Collections lists all collections with their book counts, ordered for display.
func (s *Store) Collections() ([]Collection, error) {
	rows, err := s.db.Query(`
SELECT c.id, c.name, IFNULL(c.icon,''), c.sort,
       (SELECT COUNT(1) FROM collection_book cb WHERE cb.collection_id=c.id)
FROM collection c ORDER BY c.sort, c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.Icon, &c.Sort, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateCollection(name, icon string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO collection(name,icon,sort) VALUES(?,?,
		(SELECT IFNULL(MAX(sort),0)+1 FROM collection))`, name, icon)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) DeleteCollection(id int64) error {
	_, err := s.db.Exec(`DELETE FROM collection WHERE id=?`, id)
	return err
}

// CollectionBooks returns the Calibre book ids in a collection and the
// collection's name.
func (s *Store) CollectionBooks(id int64) ([]int64, string, error) {
	var name string
	if err := s.db.QueryRow(`SELECT name FROM collection WHERE id=?`, id).Scan(&name); err != nil {
		return nil, "", err
	}
	rows, err := s.db.Query(`SELECT calibre_id FROM collection_book WHERE collection_id=? ORDER BY added_at DESC`, id)
	if err != nil {
		return nil, name, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var cid int64
		if err := rows.Scan(&cid); err != nil {
			return nil, name, err
		}
		ids = append(ids, cid)
	}
	return ids, name, rows.Err()
}

func (s *Store) AddToCollection(collectionID, calibreID int64) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO collection_book(collection_id,calibre_id,added_at)
		VALUES(?,?,?)`, collectionID, calibreID, time.Now().Unix())
	return err
}

func (s *Store) RemoveFromCollection(collectionID, calibreID int64) error {
	_, err := s.db.Exec(`DELETE FROM collection_book WHERE collection_id=? AND calibre_id=?`,
		collectionID, calibreID)
	return err
}
