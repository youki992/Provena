package database

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type PacketGroup struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Fingerprint  string    `json:"fingerprint"`
	OwnerUserID  string    `json:"owner_user_id,omitempty"`
	RequestCount int       `json:"request_count"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type CapturedPacket struct {
	ID              string    `json:"id"`
	GroupID         string    `json:"group_id"`
	Method          string    `json:"method"`
	Scheme          string    `json:"scheme"`
	Host            string    `json:"host"`
	Port            int       `json:"port"`
	Path            string    `json:"path"`
	Query           string    `json:"query"`
	RequestHeaders  string    `json:"request_headers"`
	RequestBody     []byte    `json:"request_body,omitempty"`
	ResponseStatus  int       `json:"response_status"`
	ResponseHeaders string    `json:"response_headers"`
	ResponseBody    []byte    `json:"response_body,omitempty"`
	ContentType     string    `json:"content_type"`
	SessionKey      string    `json:"session_key,omitempty"`
	ScopeStatus     string    `json:"scope_status"`
	Source          string    `json:"source"`
	CreatedAt       time.Time `json:"created_at"`
}

func PacketGroupFingerprint(method, scheme, host string, port int, path, query string) string {
	keys := ""
	if parsed, err := url.ParseQuery(query); err == nil {
		parts := make([]string, 0, len(parsed))
		for key := range parsed {
			parts = append(parts, key)
		}
		for i := 0; i < len(parts); i++ {
			for j := i + 1; j < len(parts); j++ {
				if parts[j] < parts[i] {
					parts[i], parts[j] = parts[j], parts[i]
				}
			}
		}
		keys = strings.Join(parts, ",")
	}
	value := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s\x00%s", strings.ToUpper(method), strings.ToLower(scheme), strings.ToLower(host), port, path, keys)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func headerJSON(headers map[string][]string) string {
	data, _ := json.Marshal(headers)
	return string(data)
}

func (db *DB) SaveCapturedPacket(packet *CapturedPacket, ownerUserID string) (string, error) {
	if db == nil || db.DB == nil || packet == nil {
		return "", fmt.Errorf("抓包存储未初始化")
	}
	now := time.Now().UTC()
	if packet.CreatedAt.IsZero() {
		packet.CreatedAt = now
	}
	if packet.ID == "" {
		packet.ID = uuid.NewString()
	}
	if packet.ScopeStatus == "" {
		packet.ScopeStatus = "unknown"
	}
	if packet.Source == "" {
		packet.Source = "proxy"
	}
	fingerprint := PacketGroupFingerprint(packet.Method, packet.Scheme, packet.Host, packet.Port, packet.Path, packet.Query)
	name := fmt.Sprintf("%s %s%s", strings.ToUpper(packet.Method), packet.Host, packet.Path)
	if len([]rune(name)) > 180 {
		name = string([]rune(name)[:180])
	}
	_, err := db.Exec(`INSERT INTO packet_groups
		(id,name,description,fingerprint,owner_user_id,request_count,last_seen_at,created_at,updated_at)
		VALUES(?,?,?,?,?,1,?,?,?)
		ON CONFLICT(owner_user_id,fingerprint) DO UPDATE SET
		request_count=packet_groups.request_count+1,last_seen_at=excluded.last_seen_at,updated_at=excluded.updated_at`,
		uuid.NewString(), name, "按方法、主机、路径和查询参数键自动归纳", fingerprint, strings.TrimSpace(ownerUserID), now, now, now)
	if err != nil {
		return "", fmt.Errorf("创建抓包分组失败: %w", err)
	}
	var groupID string
	if err := db.QueryRow(`SELECT id FROM packet_groups WHERE owner_user_id=? AND fingerprint=?`, strings.TrimSpace(ownerUserID), fingerprint).Scan(&groupID); err != nil {
		return "", fmt.Errorf("读取抓包分组失败: %w", err)
	}
	packet.GroupID = groupID
	_, err = db.Exec(`INSERT INTO captured_packets
		(id,group_id,method,scheme,host,port,path,query,request_headers,request_body,response_status,response_headers,response_body,content_type,session_key,scope_status,source,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, packet.ID, packet.GroupID, packet.Method, packet.Scheme, packet.Host, packet.Port, packet.Path, packet.Query, packet.RequestHeaders, packet.RequestBody, packet.ResponseStatus, packet.ResponseHeaders, packet.ResponseBody, packet.ContentType, packet.SessionKey, packet.ScopeStatus, packet.Source, packet.CreatedAt)
	if err != nil {
		return "", fmt.Errorf("保存抓包失败: %w", err)
	}
	return groupID, nil
}

func (db *DB) ListPacketGroups(ownerUserID string, limit int) ([]*PacketGroup, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.Query(`SELECT id,name,description,fingerprint,owner_user_id,request_count,last_seen_at,created_at,updated_at FROM packet_groups WHERE owner_user_id=? ORDER BY last_seen_at DESC LIMIT ?`, strings.TrimSpace(ownerUserID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*PacketGroup{}
	for rows.Next() {
		item := &PacketGroup{}
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Fingerprint, &item.OwnerUserID, &item.RequestCount, &item.LastSeenAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (db *DB) GetPacketGroup(id, ownerUserID string) (*PacketGroup, error) {
	item := &PacketGroup{}
	err := db.QueryRow(`SELECT id,name,description,fingerprint,owner_user_id,request_count,last_seen_at,created_at,updated_at FROM packet_groups WHERE id=? AND owner_user_id=?`, id, strings.TrimSpace(ownerUserID)).Scan(&item.ID, &item.Name, &item.Description, &item.Fingerprint, &item.OwnerUserID, &item.RequestCount, &item.LastSeenAt, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (db *DB) ListCapturedPackets(groupID, ownerUserID string, limit int) ([]*CapturedPacket, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.Query(`SELECT p.id,p.group_id,p.method,p.scheme,p.host,p.port,p.path,p.query,p.request_headers,p.request_body,p.response_status,p.response_headers,p.response_body,p.content_type,p.session_key,p.scope_status,p.source,p.created_at FROM captured_packets p JOIN packet_groups g ON g.id=p.group_id WHERE p.group_id=? AND g.owner_user_id=? ORDER BY p.created_at DESC LIMIT ?`, groupID, strings.TrimSpace(ownerUserID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*CapturedPacket{}
	for rows.Next() {
		item := &CapturedPacket{}
		if err := rows.Scan(&item.ID, &item.GroupID, &item.Method, &item.Scheme, &item.Host, &item.Port, &item.Path, &item.Query, &item.RequestHeaders, &item.RequestBody, &item.ResponseStatus, &item.ResponseHeaders, &item.ResponseBody, &item.ContentType, &item.SessionKey, &item.ScopeStatus, &item.Source, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (db *DB) GetCapturedPacket(id, ownerUserID string) (*CapturedPacket, error) {
	item := &CapturedPacket{}
	err := db.QueryRow(`SELECT p.id,p.group_id,p.method,p.scheme,p.host,p.port,p.path,p.query,p.request_headers,p.request_body,p.response_status,p.response_headers,p.response_body,p.content_type,p.session_key,p.scope_status,p.source,p.created_at FROM captured_packets p JOIN packet_groups g ON g.id=p.group_id WHERE p.id=? AND g.owner_user_id=?`, id, strings.TrimSpace(ownerUserID)).Scan(&item.ID, &item.GroupID, &item.Method, &item.Scheme, &item.Host, &item.Port, &item.Path, &item.Query, &item.RequestHeaders, &item.RequestBody, &item.ResponseStatus, &item.ResponseHeaders, &item.ResponseBody, &item.ContentType, &item.SessionKey, &item.ScopeStatus, &item.Source, &item.CreatedAt)
	if err != nil {
		return nil, err
	}
	return item, nil
}
