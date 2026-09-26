-- name: CreateExampleItem :one
INSERT INTO example_items (name)
VALUES ($1)
RETURNING id, name, created_at;

-- name: GetExampleItem :one
SELECT id, name, created_at
FROM example_items
WHERE id = $1;

-- name: ListExampleItems :many
SELECT id, name, created_at
FROM example_items
ORDER BY id
LIMIT 100;

-- name: DeleteExampleItem :exec
DELETE FROM example_items
WHERE id = $1;
