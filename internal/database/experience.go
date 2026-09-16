package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ExperienceEntry is a durable lesson candidate. Automatic observations are
// always pending until the evidence controller or a human explicitly verifies
// them; only verified entries may be promoted to the searchable knowledge base.
type ExperienceEntry struct {
	ID                   string    `json:"id"`
	Title                string    `json:"title"`
	Category             string    `json:"category"`
	Content              string    `json:"content"`
	EvidenceRefs         []string  `json:"evidence_refs"`
	ProjectID            string    `json:"project_id,omitempty"`
	SourceConversationID string    `json:"source_conversation_id,omitempty"`
	SourceFGSPath        string    `json:"source_fgs_path,omitempty"`
	SourceNodeIDs        []string  `json:"source_node_ids"`
	VerificationStatus   string    `json:"verification_status"` // pending | verified | human_confirmed | rejected
	VerifiedBy           string    `json:"verified_by,omitempty"`
	VerifiedAt           time.Time `json:"verified_at,omitempty"`
	PromotedPath         string    `json:"promoted_path,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func (db *DB) CreateExperience(e *ExperienceEntry) (*ExperienceEntry, error) {
	if db == nil || e == nil {
		return nil, fmt.Errorf("experience entry is nil")
	}
	if strings.TrimSpace(e.ID) == "" {
		e.ID = uuid.New().String()
	}
	if strings.TrimSpace(e.VerificationStatus) == "" {
		e.VerificationStatus = "pending"
	}
	if e.VerificationStatus != "pending" && e.VerificationStatus != "verified" && e.VerificationStatus != "human_confirmed" && e.VerificationStatus != "rejected" {
		return nil, fmt.Errorf("无效的经验验证状态: %s", e.VerificationStatus)
	}
	now := time.Now().UTC()
	e.CreatedAt, e.UpdatedAt = now, now
	_, err := db.Exec(`INSERT INTO experience_entries
		(id,title,category,content,evidence_refs,project_id,source_conversation_id,source_fgs_path,source_node_ids,verification_status,verified_by,verified_at,promoted_path,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.Title, e.Category, e.Content, marshalStringList(e.EvidenceRefs), e.ProjectID,
		e.SourceConversationID, e.SourceFGSPath, marshalStringList(e.SourceNodeIDs), e.VerificationStatus,
		e.VerifiedBy, nullableTime(e.VerifiedAt), e.PromotedPath, e.CreatedAt, e.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("创建经验条目失败: %w", err)
	}
	return e, nil
}

func (db *DB) ListExperiences(status, projectID string, limit, offset int) ([]*ExperienceEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	query := `SELECT id,title,category,content,evidence_refs,project_id,source_conversation_id,source_fgs_path,source_node_ids,verification_status,verified_by,verified_at,promoted_path,created_at,updated_at FROM experience_entries WHERE 1=1`
	args := []interface{}{}
	if strings.TrimSpace(status) != "" {
		query += " AND verification_status = ?"
		args = append(args, strings.TrimSpace(status))
	}
	if strings.TrimSpace(projectID) != "" {
		query += " AND project_id = ?"
		args = append(args, strings.TrimSpace(projectID))
	}
	query += " ORDER BY updated_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("列出经验条目失败: %w", err)
	}
	defer rows.Close()
	var out []*ExperienceEntry
	for rows.Next() {
		e, err := scanExperience(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (db *DB) GetExperience(id string) (*ExperienceEntry, error) {
	row := db.QueryRow(`SELECT id,title,category,content,evidence_refs,project_id,source_conversation_id,source_fgs_path,source_node_ids,verification_status,verified_by,verified_at,promoted_path,created_at,updated_at FROM experience_entries WHERE id = ?`, strings.TrimSpace(id))
	e, err := scanExperience(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("经验条目不存在")
	}
	if err != nil {
		return nil, fmt.Errorf("获取经验条目失败: %w", err)
	}
	return e, nil
}

func (db *DB) SetExperienceVerification(id, status, verifiedBy string) (*ExperienceEntry, error) {
	status = strings.TrimSpace(status)
	if status != "verified" && status != "human_confirmed" && status != "rejected" && status != "pending" {
		return nil, fmt.Errorf("无效的经验验证状态: %s", status)
	}
	verifiedAt := interface{}(nil)
	if status == "verified" || status == "human_confirmed" {
		verifiedAt = time.Now().UTC()
	}
	res, err := db.Exec(`UPDATE experience_entries SET verification_status=?, verified_by=?, verified_at=?, updated_at=? WHERE id=?`, status, strings.TrimSpace(verifiedBy), verifiedAt, time.Now().UTC(), strings.TrimSpace(id))
	if err != nil {
		return nil, fmt.Errorf("更新经验验证状态失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("经验条目不存在")
	}
	return db.GetExperience(id)
}

func (db *DB) MarkExperiencePromoted(id, path string) error {
	res, err := db.Exec(`UPDATE experience_entries SET promoted_path=?, updated_at=? WHERE id=? AND verification_status IN ('verified','human_confirmed')`, strings.TrimSpace(path), time.Now().UTC(), strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("记录经验晋级失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("经验不存在或尚未通过验证")
	}
	return nil
}

// VerifyExperiencesForFGS is called only by the deterministic FGS controller
// after an independent evaluator selected evidence-bearing node IDs. It never
// upgrades rejected or human-confirmed entries and leaves all other candidates
// pending for human review.
func (db *DB) VerifyExperiencesForFGS(path string, nodeIDs []string, verifiedBy string) (int, error) {
	updated := 0
	for _, nodeID := range nodeIDs {
		res, err := db.Exec(`UPDATE experience_entries SET verification_status='verified', verified_by=?, verified_at=?, updated_at=? WHERE source_fgs_path=? AND source_node_ids LIKE ? AND verification_status='pending'`,
			strings.TrimSpace(verifiedBy), time.Now().UTC(), time.Now().UTC(), strings.TrimSpace(path), "%"+strings.TrimSpace(nodeID)+"%")
		if err != nil {
			return updated, fmt.Errorf("自动验证经验失败: %w", err)
		}
		n, _ := res.RowsAffected()
		updated += int(n)
	}
	return updated, nil
}

func (db *DB) ExperienceExistsBySource(path, nodeID string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM experience_entries WHERE source_fgs_path=? AND source_node_ids LIKE ?`, strings.TrimSpace(path), "%"+strings.TrimSpace(nodeID)+"%").Scan(&n)
	return n > 0, err
}

type experienceScanner interface{ Scan(...interface{}) error }

func scanExperience(s experienceScanner) (*ExperienceEntry, error) {
	var e ExperienceEntry
	var evidence, nodeIDs, createdAt, updatedAt string
	var verifiedAt sql.NullString
	if err := s.Scan(&e.ID, &e.Title, &e.Category, &e.Content, &evidence, &e.ProjectID, &e.SourceConversationID, &e.SourceFGSPath, &nodeIDs, &e.VerificationStatus, &e.VerifiedBy, &verifiedAt, &e.PromotedPath, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	e.EvidenceRefs = unmarshalStringList(evidence)
	e.SourceNodeIDs = unmarshalStringList(nodeIDs)
	if verifiedAt.Valid {
		e.VerifiedAt = parseDBTime(verifiedAt.String)
	}
	e.CreatedAt = parseDBTime(createdAt)
	e.UpdatedAt = parseDBTime(updatedAt)
	return &e, nil
}

func nullableTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}

func marshalStringList(values []string) string {
	b, _ := json.Marshal(values)
	return string(b)
}

func unmarshalStringList(raw string) []string {
	var out []string
	if json.Unmarshal([]byte(raw), &out) != nil || out == nil {
		return []string{}
	}
	return out
}
