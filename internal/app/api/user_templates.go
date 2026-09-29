package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// User templates are plan presets: a name, a data limit and a duration a
// reseller bot offers for sale and then creates the user from. The shape here
// is the one Marzban's API uses, because that is what those bots speak; a
// panel without this endpoint leaves them with no plans to list.
//
// This panel expresses "which hosts a user gets" as a service, so a template
// may point at one. The `inbounds` field is kept for round-trip fidelity with
// clients that send it, and is otherwise derived from the linked service.

const (
	userTemplateNameMaxLength   = 64
	userTemplateAffixMaxLength  = 20
	userTemplateMaxRequestBytes = 1 << 20
)

type userTemplateResponse struct {
	ID             int64               `json:"id"`
	Name           string              `json:"name"`
	DataLimit      int64               `json:"data_limit"`
	ExpireDuration int64               `json:"expire_duration"`
	UsernamePrefix *string             `json:"username_prefix"`
	UsernameSuffix *string             `json:"username_suffix"`
	Inbounds       map[string][]string `json:"inbounds"`
	ServiceID      *int64              `json:"service_id,omitempty"`
	ServiceName    *string             `json:"service_name,omitempty"`
}

type userTemplatePayload struct {
	Name           *string             `json:"name"`
	DataLimit      *int64              `json:"data_limit"`
	ExpireDuration *int64              `json:"expire_duration"`
	UsernamePrefix *string             `json:"username_prefix"`
	UsernameSuffix *string             `json:"username_suffix"`
	Inbounds       map[string][]string `json:"inbounds"`
	ServiceID      *int64              `json:"service_id"`
}

type userTemplateRow struct {
	ID             int64
	Name           string
	DataLimit      int64
	ExpireDuration int64
	UsernamePrefix sql.NullString
	UsernameSuffix sql.NullString
	ServiceID      sql.NullInt64
	Inbounds       sql.NullString
}

func (s *Server) handleUserTemplatesRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleUserTemplateList(w, r)
	case http.MethodPost:
		s.handleUserTemplateCreate(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleUserTemplatePath(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/user_template/"), "/")
	if rest == "" {
		s.handleUserTemplatesRoot(w, r)
		return
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "User template not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleUserTemplateGet(w, r, id)
	case http.MethodPut:
		s.handleUserTemplateUpdate(w, r, id)
	case http.MethodDelete:
		s.handleUserTemplateDelete(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleUserTemplateList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), userTemplateSelectSQL+` ORDER BY id`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	templates := []userTemplateResponse{}
	for rows.Next() {
		var row userTemplateRow
		if err := scanUserTemplate(rows, &row); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		response, err := s.userTemplateResponse(r.Context(), row)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		templates = append(templates, response)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

func (s *Server) handleUserTemplateGet(w http.ResponseWriter, r *http.Request, id int64) {
	row, err := s.userTemplateRow(r.Context(), id)
	if err != nil {
		writeUserTemplateError(w, err)
		return
	}
	response, err := s.userTemplateResponse(r.Context(), row)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleUserTemplateCreate(w http.ResponseWriter, r *http.Request) {
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	payload, err := decodeUserTemplatePayload(w, r)
	if err != nil {
		writeUserTemplateError(w, err)
		return
	}
	if payload.Name == nil || strings.TrimSpace(*payload.Name) == "" {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	row, err := normalizeUserTemplatePayload(payload, userTemplateRow{})
	if err != nil {
		writeUserTemplateError(w, err)
		return
	}
	if err := s.ensureTemplateServiceExists(r.Context(), row.ServiceID); err != nil {
		writeUserTemplateError(w, err)
		return
	}

	now := dbTimestamp(time.Now().UTC())
	result, err := s.db.ExecContext(r.Context(), `INSERT INTO reseller_plan_templates
(name, data_limit, expire_duration, username_prefix, username_suffix, service_id, inbounds, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.Name, row.DataLimit, row.ExpireDuration, templateNullableString(row.UsernamePrefix),
		templateNullableString(row.UsernameSuffix), templateNullableInt64(row.ServiceID), templateNullableString(row.Inbounds), now, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	stored, err := s.userTemplateRow(r.Context(), id)
	if err != nil {
		writeUserTemplateError(w, err)
		return
	}
	response, err := s.userTemplateResponse(r.Context(), stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) handleUserTemplateUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	current, err := s.userTemplateRow(r.Context(), id)
	if err != nil {
		writeUserTemplateError(w, err)
		return
	}
	payload, err := decodeUserTemplatePayload(w, r)
	if err != nil {
		writeUserTemplateError(w, err)
		return
	}
	// A partial update keeps whatever the caller left out, so a bot that
	// sends only the fields it changed does not blank the rest.
	row, err := normalizeUserTemplatePayload(payload, current)
	if err != nil {
		writeUserTemplateError(w, err)
		return
	}
	if err := s.ensureTemplateServiceExists(r.Context(), row.ServiceID); err != nil {
		writeUserTemplateError(w, err)
		return
	}

	if _, err := s.db.ExecContext(r.Context(), `UPDATE reseller_plan_templates
SET name = ?, data_limit = ?, expire_duration = ?, username_prefix = ?, username_suffix = ?, service_id = ?, inbounds = ?, updated_at = ?
WHERE id = ?`,
		row.Name, row.DataLimit, row.ExpireDuration, templateNullableString(row.UsernamePrefix),
		templateNullableString(row.UsernameSuffix), templateNullableInt64(row.ServiceID), templateNullableString(row.Inbounds),
		dbTimestamp(time.Now().UTC()), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	stored, err := s.userTemplateRow(r.Context(), id)
	if err != nil {
		writeUserTemplateError(w, err)
		return
	}
	response, err := s.userTemplateResponse(r.Context(), stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleUserTemplateDelete(w http.ResponseWriter, r *http.Request, id int64) {
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	if _, err := s.userTemplateRow(r.Context(), id); err != nil {
		writeUserTemplateError(w, err)
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM reseller_plan_templates WHERE id = ?`, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"detail": "User template removed"})
}

const userTemplateSelectSQL = `SELECT id, name, data_limit, expire_duration, username_prefix, username_suffix, service_id, inbounds FROM reseller_plan_templates`

func scanUserTemplate(scanner rowScanner, row *userTemplateRow) error {
	return scanner.Scan(&row.ID, &row.Name, &row.DataLimit, &row.ExpireDuration,
		&row.UsernamePrefix, &row.UsernameSuffix, &row.ServiceID, &row.Inbounds)
}

func (s *Server) userTemplateRow(ctx context.Context, id int64) (userTemplateRow, error) {
	var row userTemplateRow
	err := scanUserTemplate(s.db.QueryRowContext(ctx, userTemplateSelectSQL+` WHERE id = ?`, id), &row)
	if err == sql.ErrNoRows {
		return userTemplateRow{}, statusError{status: http.StatusNotFound, detail: "User template not found"}
	}
	return row, err
}

func (s *Server) ensureTemplateServiceExists(ctx context.Context, serviceID sql.NullInt64) error {
	if !serviceID.Valid {
		return nil
	}
	return ensureServiceExistsDB(ctx, s.db, serviceID.Int64)
}

// userTemplateResponse fills in the inbounds a client expects. A template that
// was stored with explicit inbounds reports those; otherwise they are derived
// from the linked service's hosts, so a bot sees which protocols the plan
// actually hands out.
func (s *Server) userTemplateResponse(ctx context.Context, row userTemplateRow) (userTemplateResponse, error) {
	response := userTemplateResponse{
		ID:             row.ID,
		Name:           row.Name,
		DataLimit:      row.DataLimit,
		ExpireDuration: row.ExpireDuration,
		Inbounds:       map[string][]string{},
	}
	if row.UsernamePrefix.Valid && row.UsernamePrefix.String != "" {
		prefix := row.UsernamePrefix.String
		response.UsernamePrefix = &prefix
	}
	if row.UsernameSuffix.Valid && row.UsernameSuffix.String != "" {
		suffix := row.UsernameSuffix.String
		response.UsernameSuffix = &suffix
	}
	if row.ServiceID.Valid {
		serviceID := row.ServiceID.Int64
		response.ServiceID = &serviceID
		var name string
		if err := s.db.QueryRowContext(ctx, `SELECT name FROM services WHERE id = ?`, serviceID).Scan(&name); err == nil {
			response.ServiceName = &name
		} else if err != sql.ErrNoRows {
			return userTemplateResponse{}, err
		}
	}
	if row.Inbounds.Valid && strings.TrimSpace(row.Inbounds.String) != "" {
		stored := map[string][]string{}
		if err := json.Unmarshal([]byte(row.Inbounds.String), &stored); err == nil && len(stored) > 0 {
			response.Inbounds = stored
			return response, nil
		}
	}
	if row.ServiceID.Valid {
		derived, err := s.serviceInboundsByProtocol(ctx, row.ServiceID.Int64)
		if err != nil {
			return userTemplateResponse{}, err
		}
		response.Inbounds = derived
	}
	return response, nil
}

// serviceInboundsByProtocol groups a service's inbound tags by protocol, which
// is the shape Marzban's clients read.
func (s *Server) serviceInboundsByProtocol(ctx context.Context, serviceID int64) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT h.inbound_tag
FROM service_hosts sh JOIN hosts h ON h.id = sh.host_id
WHERE sh.service_id = ? AND h.inbound_tag IS NOT NULL AND h.inbound_tag != ''
ORDER BY h.inbound_tag`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grouped := map[string][]string{}
	seen := map[string]bool{}
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		protocol := "unknown"
		if inbound, err := s.configRepo.GetInbound(ctx, tag); err == nil && inbound != nil {
			if value, ok := inbound["protocol"].(string); ok && strings.TrimSpace(value) != "" {
				protocol = strings.ToLower(strings.TrimSpace(value))
			}
		}
		grouped[protocol] = append(grouped[protocol], tag)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return grouped, nil
}

func decodeUserTemplatePayload(w http.ResponseWriter, r *http.Request) (userTemplatePayload, error) {
	var payload userTemplatePayload
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, userTemplateMaxRequestBytes))
	if err := decoder.Decode(&payload); err != nil {
		return userTemplatePayload{}, statusError{status: http.StatusUnprocessableEntity, detail: "invalid request body"}
	}
	return payload, nil
}

// normalizeUserTemplatePayload validates the request and merges it onto the
// current row, so both a create (from a zero row) and a partial update go
// through exactly the same rules.
func normalizeUserTemplatePayload(payload userTemplatePayload, current userTemplateRow) (userTemplateRow, error) {
	row := current
	if payload.Name != nil {
		name := strings.TrimSpace(*payload.Name)
		if name == "" {
			return row, statusError{status: http.StatusUnprocessableEntity, detail: "name is required"}
		}
		if len([]rune(name)) > userTemplateNameMaxLength {
			return row, statusError{status: http.StatusUnprocessableEntity, detail: "name must be at most 64 characters"}
		}
		row.Name = name
	}
	if payload.DataLimit != nil {
		if *payload.DataLimit < 0 {
			return row, statusError{status: http.StatusUnprocessableEntity, detail: "data_limit must not be negative"}
		}
		row.DataLimit = *payload.DataLimit
	}
	if payload.ExpireDuration != nil {
		if *payload.ExpireDuration < 0 {
			return row, statusError{status: http.StatusUnprocessableEntity, detail: "expire_duration must not be negative"}
		}
		row.ExpireDuration = *payload.ExpireDuration
	}
	if payload.UsernamePrefix != nil {
		value := strings.TrimSpace(*payload.UsernamePrefix)
		if len([]rune(value)) > userTemplateAffixMaxLength {
			return row, statusError{status: http.StatusUnprocessableEntity, detail: "username_prefix must be at most 20 characters"}
		}
		row.UsernamePrefix = sql.NullString{String: value, Valid: value != ""}
	}
	if payload.UsernameSuffix != nil {
		value := strings.TrimSpace(*payload.UsernameSuffix)
		if len([]rune(value)) > userTemplateAffixMaxLength {
			return row, statusError{status: http.StatusUnprocessableEntity, detail: "username_suffix must be at most 20 characters"}
		}
		row.UsernameSuffix = sql.NullString{String: value, Valid: value != ""}
	}
	if payload.ServiceID != nil {
		// 0 clears the link, which is how a client detaches a plan from a
		// service without deleting the template.
		row.ServiceID = sql.NullInt64{Int64: *payload.ServiceID, Valid: *payload.ServiceID > 0}
	}
	if payload.Inbounds != nil {
		if len(payload.Inbounds) == 0 {
			row.Inbounds = sql.NullString{}
		} else {
			encoded, err := json.Marshal(payload.Inbounds)
			if err != nil {
				return row, statusError{status: http.StatusUnprocessableEntity, detail: "invalid inbounds"}
			}
			row.Inbounds = sql.NullString{String: string(encoded), Valid: true}
		}
	}
	return row, nil
}

func templateNullableString(value sql.NullString) any {
	if !value.Valid || value.String == "" {
		return nil
	}
	return value.String
}

func templateNullableInt64(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func writeUserTemplateError(w http.ResponseWriter, err error) {
	var tagged statusError
	if errors.As(err, &tagged) {
		writeError(w, tagged.status, tagged.detail)
		return
	}
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "User template not found")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
