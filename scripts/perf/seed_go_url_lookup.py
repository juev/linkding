"""Seed a disposable Go SQLite database for owner-scoped URL lookup benchmarks."""

import argparse
import sqlite3
from datetime import datetime, timezone
from pathlib import Path


parser = argparse.ArgumentParser()
parser.add_argument("database", type=Path)
parser.add_argument("token_file", type=Path)
parser.add_argument("--mode", choices=("single", "multi"), required=True)
parser.add_argument("--count", type=int, default=10_000)
args = parser.parse_args()
if args.count < 1:
    parser.error("--count must be positive")

db = sqlite3.connect(args.database)
db.execute("PRAGMA foreign_keys=ON")
owner = db.execute("SELECT id FROM auth_user WHERE username='bench'").fetchone()
if owner is None:
    raise RuntimeError("initialize the Go server with LD_SUPERUSER_NAME=bench first")
owner_id = owner[0]
if db.execute("SELECT count(*) FROM bookmarks_bookmark").fetchone()[0]:
    raise RuntimeError("fixture database already contains bookmarks")

now = datetime.now(timezone.utc).isoformat(sep=" ")
insert_bookmark = """INSERT INTO bookmarks_bookmark
    (url,url_normalized,title,description,notes,website_title,website_description,
     unread,is_archived,shared,date_added,date_modified,owner_id,
     web_archive_snapshot_url,favicon_file,preview_image_file)
    VALUES (?,?,?,'','',NULL,NULL,0,0,0,?,?,?,'','','')"""
db.execute("BEGIN IMMEDIATE")
if args.mode == "single":
    rows = (
        (
            f"https://example.org/articles/{i:05d}",
            f"https://example.org/articles/{i:05d}",
            f"Article {i:05d}",
            now,
            now,
            owner_id,
        )
        for i in range(args.count)
    )
    db.executemany(insert_bookmark, rows)
else:
    shared_url = "https://example.org/bench/shared/shared"
    insert_user = """INSERT INTO auth_user
        (password,last_login,is_superuser,username,first_name,last_name,email,is_staff,is_active,date_joined)
        VALUES ('',NULL,0,?,'','','',0,1,?)"""
    for i in range(args.count):
        user_id = db.execute(insert_user, (f"url-bench-{i:05d}", now)).lastrowid
        db.execute(
            insert_bookmark,
            (shared_url, shared_url, "Shared", now, now, user_id),
        )

token = "b" * 40
db.execute(
    "INSERT INTO bookmarks_apitoken (key,name,created,user_id) VALUES (?,?,?,?)",
    (token, "URL lookup benchmark", now, owner_id),
)
db.commit()
integrity = db.execute("PRAGMA integrity_check").fetchone()[0]
bookmark_count = db.execute("SELECT count(*) FROM bookmarks_bookmark").fetchone()[0]
stat_table = db.execute(
    "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='sqlite_stat1'"
).fetchone()[0]
db.execute("PRAGMA wal_checkpoint(TRUNCATE)")
db.close()
if integrity != "ok" or bookmark_count != args.count or stat_table:
    raise RuntimeError(
        f"fixture invalid: integrity={integrity} bookmarks={bookmark_count} sqlite_stat1={stat_table}"
    )
args.token_file.write_text(token + "\n", encoding="ascii")
print(f"seeded mode={args.mode} bookmarks={bookmark_count} integrity={integrity}")
