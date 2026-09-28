-- +goose Up
DROP INDEX bookmarks_bookmark_url_normalized_8b3c53e4;
CREATE INDEX bm_norm_owner_idx ON bookmarks_bookmark (url_normalized, owner_id);

-- +goose Down
DROP INDEX bm_norm_owner_idx;
CREATE INDEX bookmarks_bookmark_url_normalized_8b3c53e4 ON bookmarks_bookmark (url_normalized);
