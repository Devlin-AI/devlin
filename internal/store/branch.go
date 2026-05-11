package store

import (
	"database/sql"
	"fmt"
	"strings"
)

func (s *Store) CreateBranch(sessionID, parentID string, parentMsgID int64) error {
	_, err := s.db.Exec(
		"INSERT INTO branches (session_id, parent_id, parent_msg_id) VALUES (?, ?, ?)",
		sessionID, parentID, parentMsgID,
	)
	return err
}

func (s *Store) GetBranchMeta(sessionID string) (*BranchMeta, error) {
	row := s.db.QueryRow(
		"SELECT session_id, parent_id, parent_msg_id FROM branches WHERE session_id = ?",
		sessionID,
	)
	var b BranchMeta
	var parentID sql.NullString
	var parentMsgID sql.NullInt64
	if err := row.Scan(&b.SessionID, &parentID, &parentMsgID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if parentID.Valid {
		b.ParentID = parentID.String
	}
	if parentMsgID.Valid {
		b.ParentMsgID = parentMsgID.Int64
	}
	return &b, nil
}

func (s *Store) ListBranches(parentID string) ([]BranchMeta, error) {
	rows, err := s.db.Query(
		"SELECT session_id, parent_id, parent_msg_id FROM branches WHERE parent_id = ?",
		parentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var branches []BranchMeta
	for rows.Next() {
		var b BranchMeta
		var pid sql.NullString
		var pmid sql.NullInt64
		if err := rows.Scan(&b.SessionID, &pid, &pmid); err != nil {
			return nil, err
		}
		if pid.Valid {
			b.ParentID = pid.String
		}
		if pmid.Valid {
			b.ParentMsgID = pmid.Int64
		}
		branches = append(branches, b)
	}
	return branches, rows.Err()
}

func (s *Store) GetBranchChain(sessionID string) ([]BranchMeta, error) {
	rows, err := s.db.Query(`
		WITH RECURSIVE chain AS (
			SELECT session_id, parent_id, parent_msg_id FROM branches WHERE session_id = ?
			UNION ALL
			SELECT b.session_id, b.parent_id, b.parent_msg_id
			FROM branches b INNER JOIN chain c ON b.session_id = c.parent_id
		)
		SELECT session_id, parent_id, parent_msg_id FROM chain
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chain []BranchMeta
	for rows.Next() {
		var b BranchMeta
		var pid sql.NullString
		var pmid sql.NullInt64
		if err := rows.Scan(&b.SessionID, &pid, &pmid); err != nil {
			return nil, err
		}
		if pid.Valid {
			b.ParentID = pid.String
		}
		if pmid.Valid {
			b.ParentMsgID = pmid.Int64
		}
		chain = append(chain, b)
	}
	return chain, rows.Err()
}

func (s *Store) updateBranch(sessionID string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	var setClauses []string
	var args []any
	for k, v := range fields {
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", k))
		args = append(args, v)
	}
	args = append(args, sessionID)
	query := fmt.Sprintf("UPDATE branches SET %s WHERE session_id = ?", strings.Join(setClauses, ", "))
	_, err := s.db.Exec(query, args...)
	return err
}

func (s *Store) deleteBranch(sessionID string) error {
	_, err := s.db.Exec("DELETE FROM branches WHERE session_id = ?", sessionID)
	return err
}
