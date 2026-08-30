package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PlaygroundPreset is a named request configuration.
//
// Config is carried as raw JSON rather than a struct: the store is not the
// authority on what a request setting is, and decoding it here would silently
// drop any field the console had learned and this build had not.
type PlaygroundPreset struct {
	ID        string
	Name      string
	Dialect   string
	Model     string
	Config    json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

func newPlaygroundID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate playground id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func (d *DB) CreatePlaygroundPreset(
	ctx context.Context, name, dialect, model string, config json.RawMessage,
) (PlaygroundPreset, error) {
	id, err := newPlaygroundID()
	if err != nil {
		return PlaygroundPreset{}, err
	}
	now := time.Now().UTC()
	p := PlaygroundPreset{
		ID: id, Name: name, Dialect: dialect, Model: model,
		Config: config, CreatedAt: now, UpdatedAt: now,
	}
	_, err = d.Write.ExecContext(ctx,
		`INSERT INTO playground_presets (id, name, dialect, model, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Dialect, p.Model, string(p.Config), now.Unix(), now.Unix())
	if err != nil {
		return PlaygroundPreset{}, fmt.Errorf("store playground preset: %w", err)
	}
	return p, nil
}

func (d *DB) PlaygroundPresets(ctx context.Context) ([]PlaygroundPreset, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, name, dialect, model, config, created_at, updated_at
		   FROM playground_presets ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list playground presets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []PlaygroundPreset{}
	for rows.Next() {
		p, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlaygroundPresetByName finds the preset a name already belongs to, so a save
// that would clash can offer to overwrite that row rather than reporting a
// constraint failure.
func (d *DB) PlaygroundPresetByName(ctx context.Context, name string) (PlaygroundPreset, bool, error) {
	row := d.Read.QueryRowContext(ctx,
		`SELECT id, name, dialect, model, config, created_at, updated_at
		   FROM playground_presets WHERE name = ?`, name)
	p, err := scanPreset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PlaygroundPreset{}, false, nil
	}
	if err != nil {
		return PlaygroundPreset{}, false, err
	}
	return p, true, nil
}

func (d *DB) UpdatePlaygroundPreset(
	ctx context.Context, id, name, dialect, model string, config json.RawMessage,
) (bool, error) {
	res, err := d.Write.ExecContext(ctx,
		`UPDATE playground_presets
		    SET name = ?, dialect = ?, model = ?, config = ?, updated_at = ?
		  WHERE id = ?`,
		name, dialect, model, string(config), time.Now().UTC().Unix(), id)
	if err != nil {
		return false, fmt.Errorf("update playground preset: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (d *DB) DeletePlaygroundPreset(ctx context.Context, id string) (bool, error) {
	res, err := d.Write.ExecContext(ctx, `DELETE FROM playground_presets WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete playground preset: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// scanner is what *sql.Row and *sql.Rows have in common, so one scan serves
// the lookup and the listing.
type scanner interface{ Scan(dest ...any) error }

func scanPreset(s scanner) (PlaygroundPreset, error) {
	var (
		p                = PlaygroundPreset{}
		cfg              string
		created, updated int64
	)
	if err := s.Scan(&p.ID, &p.Name, &p.Dialect, &p.Model, &cfg, &created, &updated); err != nil {
		return PlaygroundPreset{}, fmt.Errorf("scan playground preset: %w", err)
	}
	p.Config = json.RawMessage(cfg)
	p.CreatedAt = time.Unix(created, 0).UTC()
	p.UpdatedAt = time.Unix(updated, 0).UTC()
	return p, nil
}

// PlaygroundConversation is a saved Chat-mode session.
//
// Config carries the same opaque blob a preset does and for the same reason:
// the store is not the authority on what a request setting is, and a struct
// here would drop whatever field the console learned after this binary was
// built.
type PlaygroundConversation struct {
	ID        string
	Title     string
	Dialect   string
	Model     string
	Config    json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
	// Preview is the most recent user turn, which is what the history rail
	// shows beneath each title. It belongs to the listing rather than to the
	// row: a single-conversation read carries the turns themselves and leaves
	// this empty.
	Preview string
}

// PlaygroundTurn is one message in a saved conversation.
//
// RequestID is empty rather than absent when the turn has no trace. Two
// ordinary situations produce that: the log writer batches on a 250ms timer so
// a turn can be stored before its trace exists, and the request log's
// retention sweep outlives plenty of conversations.
type PlaygroundTurn struct {
	ID        string
	Seq       int
	Role      string
	Content   string
	RequestID string
	CreatedAt time.Time
}

// conversationListLimit caps the history rail. Past this the rail is not the
// right retrieval tool and search would be a different feature; the cap is
// stated so it is a decision rather than the point where the query gets slow.
const conversationListLimit = 200

// previewChars bounds what the listing carries per row. The rail draws one
// line, so sending a whole prompt would be payload nobody renders.
const previewChars = 200

func (d *DB) CreatePlaygroundConversation(
	ctx context.Context, title, dialect, model string, config json.RawMessage,
) (PlaygroundConversation, error) {
	id, err := newPlaygroundID()
	if err != nil {
		return PlaygroundConversation{}, err
	}
	now := time.Now().UTC()
	c := PlaygroundConversation{
		ID: id, Title: title, Dialect: dialect, Model: model,
		Config: config, CreatedAt: now, UpdatedAt: now,
	}
	_, err = d.Write.ExecContext(ctx,
		`INSERT INTO playground_conversations
		        (id, title, dialect, model, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Title, c.Dialect, c.Model, string(c.Config), now.Unix(), now.Unix())
	if err != nil {
		return PlaygroundConversation{}, fmt.Errorf("store playground conversation: %w", err)
	}
	return c, nil
}

func (d *DB) PlaygroundConversations(ctx context.Context) ([]PlaygroundConversation, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT c.id, c.title, c.dialect, c.model, c.config, c.created_at, c.updated_at,
		        COALESCE((SELECT substr(m.content, 1, ?) FROM playground_messages m
		                   WHERE m.conversation_id = c.id AND m.role = 'user'
		                   ORDER BY m.seq DESC LIMIT 1), '')
		   FROM playground_conversations c
		  ORDER BY c.updated_at DESC
		  LIMIT ?`, previewChars, conversationListLimit)
	if err != nil {
		return nil, fmt.Errorf("list playground conversations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []PlaygroundConversation{}
	for rows.Next() {
		var (
			c                = PlaygroundConversation{}
			cfg              string
			created, updated int64
		)
		if err := rows.Scan(&c.ID, &c.Title, &c.Dialect, &c.Model, &cfg,
			&created, &updated, &c.Preview); err != nil {
			return nil, fmt.Errorf("scan playground conversation: %w", err)
		}
		c.Config = json.RawMessage(cfg)
		c.CreatedAt = time.Unix(created, 0).UTC()
		c.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) PlaygroundConversationByID(
	ctx context.Context, id string,
) (PlaygroundConversation, []PlaygroundTurn, bool, error) {
	var (
		c                = PlaygroundConversation{}
		cfg              string
		created, updated int64
	)
	err := d.Read.QueryRowContext(ctx,
		`SELECT id, title, dialect, model, config, created_at, updated_at
		   FROM playground_conversations WHERE id = ?`, id).
		Scan(&c.ID, &c.Title, &c.Dialect, &c.Model, &cfg, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return PlaygroundConversation{}, nil, false, nil
	}
	if err != nil {
		return PlaygroundConversation{}, nil, false, fmt.Errorf("read playground conversation: %w", err)
	}
	c.Config = json.RawMessage(cfg)
	c.CreatedAt = time.Unix(created, 0).UTC()
	c.UpdatedAt = time.Unix(updated, 0).UTC()

	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, seq, role, content, request_id, created_at
		   FROM playground_messages WHERE conversation_id = ? ORDER BY seq`, id)
	if err != nil {
		return PlaygroundConversation{}, nil, false, fmt.Errorf("read playground turns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	turns := []PlaygroundTurn{}
	for rows.Next() {
		var (
			t       = PlaygroundTurn{}
			request sql.NullString
			at      int64
		)
		if err := rows.Scan(&t.ID, &t.Seq, &t.Role, &t.Content, &request, &at); err != nil {
			return PlaygroundConversation{}, nil, false, fmt.Errorf("scan playground turn: %w", err)
		}
		t.RequestID = request.String
		t.CreatedAt = time.Unix(at, 0).UTC()
		turns = append(turns, t)
	}
	if err := rows.Err(); err != nil {
		return PlaygroundConversation{}, nil, false, err
	}
	return c, turns, true, nil
}

func (d *DB) UpdatePlaygroundConversation(
	ctx context.Context, id, title, dialect, model string, config json.RawMessage,
) (bool, error) {
	res, err := d.Write.ExecContext(ctx,
		`UPDATE playground_conversations
		    SET title = ?, dialect = ?, model = ?, config = ?, updated_at = ?
		  WHERE id = ?`,
		title, dialect, model, string(config), time.Now().UTC().Unix(), id)
	if err != nil {
		return false, fmt.Errorf("update playground conversation: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (d *DB) DeletePlaygroundConversation(ctx context.Context, id string) (bool, error) {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM playground_conversations WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete playground conversation: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// AppendPlaygroundTurn stores one message and moves its conversation to the
// top of the rail.
//
// The next seq is read and written inside one transaction because
// idx_playground_messages_seq is unique: two turns racing on the same
// conversation would otherwise both read the same maximum, and the second
// insert would fail on the index instead of taking the next number.
func (d *DB) AppendPlaygroundTurn(
	ctx context.Context, conversationID, role, content, requestID string,
) (PlaygroundTurn, error) {
	id, err := newPlaygroundID()
	if err != nil {
		return PlaygroundTurn{}, err
	}
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return PlaygroundTurn{}, fmt.Errorf("append playground turn: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var seq int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), -1) + 1 FROM playground_messages WHERE conversation_id = ?`,
		conversationID).Scan(&seq); err != nil {
		return PlaygroundTurn{}, fmt.Errorf("next playground turn seq: %w", err)
	}

	now := time.Now().UTC()
	// A NULL rather than an empty string, because "no trace" is genuinely
	// absent rather than a request whose id happens to be blank.
	var request any
	if requestID != "" {
		request = requestID
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO playground_messages
		        (id, conversation_id, seq, role, content, request_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, conversationID, seq, role, content, request, now.Unix()); err != nil {
		return PlaygroundTurn{}, fmt.Errorf("store playground turn: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE playground_conversations SET updated_at = ? WHERE id = ?`,
		now.Unix(), conversationID); err != nil {
		return PlaygroundTurn{}, fmt.Errorf("touch playground conversation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PlaygroundTurn{}, fmt.Errorf("append playground turn: %w", err)
	}
	return PlaygroundTurn{
		ID: id, Seq: seq, Role: role, Content: content,
		RequestID: requestID, CreatedAt: now,
	}, nil
}

// PurgePlaygroundConversations empties both tables and reports how many
// conversations it removed. The cascade takes the turns.
func (d *DB) PurgePlaygroundConversations(ctx context.Context) (int64, error) {
	res, err := d.Write.ExecContext(ctx, `DELETE FROM playground_conversations`)
	if err != nil {
		return 0, fmt.Errorf("purge playground conversations: %w", err)
	}
	return res.RowsAffected()
}

// ReapEmptyPlaygroundConversations removes conversations that never received a
// turn and are older than olderThan.
//
// The age floor is the whole of the safety here. The console creates a
// conversation with its first turn rather than before it, so an empty row means
// a client that died between two calls -- but a second console tab could have
// created one moments ago, and reaping that would delete the conversation
// somebody is typing into.
func (d *DB) ReapEmptyPlaygroundConversations(
	ctx context.Context, olderThan time.Time,
) (int64, error) {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM playground_conversations
		  WHERE created_at < ?
		    AND NOT EXISTS (SELECT 1 FROM playground_messages m
		                     WHERE m.conversation_id = playground_conversations.id)`,
		olderThan.Unix())
	if err != nil {
		return 0, fmt.Errorf("reap empty playground conversations: %w", err)
	}
	return res.RowsAffected()
}
