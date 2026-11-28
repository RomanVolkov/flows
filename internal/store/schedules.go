package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/romanvolkov/flows/internal/schedule"
)

func (s *Store) PutSchedule(ctx context.Context, value schedule.Schedule) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO workflow_schedules(id,schedule_json,updated_at) VALUES(?,?,?)
		ON CONFLICT(id) DO UPDATE SET schedule_json=excluded.schedule_json,updated_at=excluded.updated_at`, value.ID, string(data), time.Now().UnixNano())
	return err
}

func (s *Store) ListSchedules(ctx context.Context) ([]schedule.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT schedule_json FROM workflow_schedules ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]schedule.Schedule, 0)
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var value schedule.Schedule
		if err := json.Unmarshal([]byte(data), &value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM workflow_schedules WHERE id=?", id)
	return err
}
