package store

import (
	"database/sql"
	"fmt"
	"strings"
)

func (r *repo) insertBranch(sessionID, parentID string, parentMsgID int64) error {
	_, err := r.db.Exec(
		"INSERT INTO branches (session_id, parent_id, parent_msg_id) VALUES (?, ?, ?)",
		sessionID, parentID, parentMsgID,
	)
	return err
}

func (r *repo) getBranch(sessionID string) (*BranchMeta, error) {
	row := r.db.QueryRow(
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

func (r *repo) updateBranch(sessionID string, fields map[string]any) error {
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
	_, err := r.db.Exec(query, args...)
	return err
}

func (r *repo) deleteBranch(sessionID string) error {
	_, err := r.db.Exec("DELETE FROM branches WHERE session_id = ?", sessionID)
	return err
}

func (r *repo) findBranchChain(sessionID string) ([]BranchMeta, error) {
	rows, err := r.db.Query(`
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
		if err := rows.Scan(&b.SessionID, &b.ParentID, &b.ParentMsgID); err != nil {
			return nil, err
		}
		chain = append(chain, b)
	}
	return chain, rows.Err()
}

func (r *repo) findBranches(parentID string) ([]BranchMeta, error) {
	rows, err := r.db.Query(
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
		if err := rows.Scan(&b.SessionID, &b.ParentID, &b.ParentMsgID); err != nil {
			return nil, err
		}
		branches = append(branches, b)
	}
	return branches, rows.Err()
}
