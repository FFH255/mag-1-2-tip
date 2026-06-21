package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"prac_15/services/tasks/internal/service"
)

var _ service.TaskRepository = (*PostgresRepository)(nil)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, t service.Task) error {
	_, err := r.pool.Exec(ctx, `
        INSERT INTO tasks (id, title, description, due_date, done)
        VALUES ($1, $2, $3, $4, $5)
    `, t.ID, t.Title, t.Description, t.DueDate, t.Done)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}
	return nil
}

func (r *PostgresRepository) List(ctx context.Context) ([]service.Task, error) {
	rows, err := r.pool.Query(ctx, `
        SELECT id, title, description, due_date, done
        FROM tasks
        ORDER BY created_at
    `)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()

	return scanTasks(rows)
}

func (r *PostgresRepository) Get(ctx context.Context, id string) (service.Task, error) {
	var t service.Task
	err := r.pool.QueryRow(ctx, `
        SELECT id, title, description, due_date, done
        FROM tasks
        WHERE id = $1
    `, id).Scan(&t.ID, &t.Title, &t.Description, &t.DueDate, &t.Done)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.Task{}, service.ErrNotFound
	}
	if err != nil {
		return service.Task{}, fmt.Errorf("get task: %w", err)
	}
	return t, nil
}

func (r *PostgresRepository) Update(ctx context.Context, t service.Task) error {
	tag, err := r.pool.Exec(ctx, `
        UPDATE tasks
        SET title = $2, description = $3, due_date = $4, done = $5
        WHERE id = $1
    `, t.ID, t.Title, t.Description, t.DueDate, t.Done)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return service.ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return service.ErrNotFound
	}
	return nil
}

// SearchByTitle выполняет параметризованный запрос: пользовательский ввод
// передаётся через $1 и никогда не склеивается со строкой SQL — это защищает
// от SQL-инъекций.
func (r *PostgresRepository) SearchByTitle(ctx context.Context, title string) ([]service.Task, error) {
	rows, err := r.pool.Query(ctx, `
        SELECT id, title, description, due_date, done
        FROM tasks
        WHERE title = $1
        ORDER BY created_at
    `, title)
	if err != nil {
		return nil, fmt.Errorf("search tasks: %w", err)
	}
	defer rows.Close()

	return scanTasks(rows)
}

func scanTasks(rows pgx.Rows) ([]service.Task, error) {
	result := make([]service.Task, 0)
	for rows.Next() {
		var t service.Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.DueDate, &t.Done); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		result = append(result, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows err: %w", err)
	}
	return result, nil
}
