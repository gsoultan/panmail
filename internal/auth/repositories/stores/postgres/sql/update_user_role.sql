UPDATE users
SET role = $1, updated_at = $2
WHERE id = $3;
